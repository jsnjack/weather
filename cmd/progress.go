package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

// Progress is the small surface shared by every long-running fetch in this
// package. Workers call Inc(1) as units complete; the work driver calls
// AddTotal as soon as it knows how many units a phase will run. Finish is
// called by whoever created the progress to tear it down. Implementations
// must be safe to use concurrently — Inc/AddTotal are typically called from
// goroutines holding a sem slot.
type Progress interface {
	AddTotal(n int)
	Inc(n int)
	Finish()
}

// NoopProgress satisfies Progress without doing anything. Used when callers
// don't want UI (e.g. JSON endpoints don't need a bar in the response body).
type noopProgress struct{}

func (noopProgress) AddTotal(int) {}
func (noopProgress) Inc(int)      {}
func (noopProgress) Finish()      {}

// NoProgress is the shared no-op singleton.
var NoProgress Progress = noopProgress{}

// ---------- CLI ----------

// CLIProgress redraws a "[####    ] 120/441 label" line on stderr using \r
// so it overwrites in place. If stderr isn't a TTY, all updates become
// no-ops so we don't garbage up piped output.
type CLIProgress struct {
	label string
	total atomic.Int64
	n     atomic.Int64
	out   io.Writer
	tty   bool
	done  chan struct{}
	once  sync.Once
	wg    sync.WaitGroup
}

func NewCLIProgress(label string) *CLIProgress {
	p := &CLIProgress{
		label: label,
		out:   os.Stderr,
		tty:   term.IsTerminal(int(os.Stderr.Fd())),
		done:  make(chan struct{}),
	}
	if p.tty {
		p.wg.Add(1)
		go p.run()
	}
	return p
}

func (p *CLIProgress) AddTotal(n int) { p.total.Add(int64(n)) }
func (p *CLIProgress) Inc(n int)      { p.n.Add(int64(n)) }

func (p *CLIProgress) Finish() {
	p.once.Do(func() { close(p.done) })
	p.wg.Wait()
	if p.tty {
		// Erase the line so the subsequent stdout output starts clean.
		fmt.Fprint(p.out, "\r\033[2K")
	}
}

func (p *CLIProgress) run() {
	defer p.wg.Done()
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-t.C:
			p.render()
		}
	}
}

func (p *CLIProgress) render() {
	total := p.total.Load()
	n := p.n.Load()
	const width = 24
	var bar string
	if total > 0 {
		filled := int(n * int64(width) / total)
		if filled > width {
			filled = width
		}
		bar = "[" + strings.Repeat("#", filled) + strings.Repeat(" ", width-filled) + "]"
	} else {
		// Indeterminate — bounce a single # across the bar.
		pos := int(time.Now().UnixMilli()/100) % (width*2 - 2)
		if pos >= width {
			pos = width*2 - 2 - pos
		}
		bar = "[" + strings.Repeat(" ", pos) + "#" + strings.Repeat(" ", width-pos-1) + "]"
	}
	fmt.Fprintf(p.out, "\r%s %d/%d %s", bar, n, total, p.label)
}

// ---------- HTTP ----------

// HTTPProgress streams `<script>__p(n,total)</script>` snippets into a
// flushed HTTP response. The browser executes each one as it arrives, so
// the user sees a live <progress> bar update.
//
// The renderer runs in a single goroutine that owns w; callers (workers)
// only ever touch atomic counters via Inc/AddTotal.
type HTTPProgress struct {
	w       io.Writer
	flusher http.Flusher
	total   atomic.Int64
	n       atomic.Int64
	done    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

func NewHTTPProgress(w http.ResponseWriter, flusher http.Flusher) *HTTPProgress {
	p := &HTTPProgress{
		w:       w,
		flusher: flusher,
		done:    make(chan struct{}),
	}
	p.wg.Add(1)
	go p.run()
	return p
}

func (p *HTTPProgress) AddTotal(n int) { p.total.Add(int64(n)) }
func (p *HTTPProgress) Inc(n int)      { p.n.Add(int64(n)) }

func (p *HTTPProgress) Finish() {
	p.once.Do(func() { close(p.done) })
	p.wg.Wait()
	// Final update + hide. After this returns, the main handler goroutine
	// owns w again and can write the body template.
	total := p.total.Load()
	fmt.Fprintf(p.w, `<script>__p(%d,%d);__pDone()</script>`, total, total)
	p.flusher.Flush()
}

func (p *HTTPProgress) run() {
	defer p.wg.Done()
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	var lastN, lastT int64 = -1, -1
	emit := func() {
		n, total := p.n.Load(), p.total.Load()
		if n == lastN && total == lastT {
			return
		}
		fmt.Fprintf(p.w, `<script>__p(%d,%d)</script>`, n, total)
		p.flusher.Flush()
		lastN, lastT = n, total
	}
	for {
		select {
		case <-p.done:
			return
		case <-t.C:
			emit()
		}
	}
}
