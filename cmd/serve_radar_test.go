package cmd

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"testing"
)

func TestCentreKNMIRadarMap(t *testing.T) {
	sea := color.RGBA{220, 225, 235, 255}
	land := color.RGBA{185, 199, 214, 255}
	label := color.RGBA{229, 241, 254, 255}
	rain := color.RGBA{28, 168, 243, 255}
	palette := color.Palette{sea, land, label, rain}
	fill := func(frame *image.Paletted, r image.Rectangle, c color.Color) {
		draw.Draw(frame, r, image.NewUniform(c), image.Point{}, draw.Src)
	}
	// The first frame is the whole map: the Netherlands' extent as land and the
	// time label at the top left. The second updates only a patch with rain,
	// as KNMI's partial frames do.
	first := image.NewPaletted(image.Rectangle{Max: knmiRadarFullSize}, palette)
	fill(first, first.Bounds(), sea)
	fill(first, image.Rect(200, 40, 706, 726), land)
	fill(first, knmiRadarLabel, label)
	second := image.NewPaletted(image.Rect(400, 300, 500, 400), palette)
	fill(second, second.Bounds(), rain)
	var source bytes.Buffer
	if err := gif.EncodeAll(&source, &gif.GIF{
		Image:     []*image.Paletted{first, second},
		Delay:     []int{30, 90},
		LoopCount: 0,
		Config:    image.Config{ColorModel: palette, Width: knmiRadarFullSize.X, Height: knmiRadarFullSize.Y},
	}); err != nil {
		t.Fatal(err)
	}

	body, err := centreKNMIRadarMap(source.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	centred, err := gif.DecodeAll(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if centred.Config.Width != 605 || centred.Config.Height != 765 {
		t.Fatalf("size=%dx%d, want 605x765", centred.Config.Width, centred.Config.Height)
	}
	if len(centred.Image) != 2 || centred.Delay[0] != 30 || centred.Delay[1] != 90 {
		t.Fatalf("frames=%d delays=%v, want 2 frames with delays [30 90]", len(centred.Image), centred.Delay)
	}
	for _, tc := range []struct {
		name  string
		frame int
		at    image.Point
		want  color.RGBA
	}{
		{"centre is land", 0, image.Pt(302, 382), land},
		{"west coast", 0, image.Pt(50, 382), land},
		{"sea west of the coast", 0, image.Pt(49, 382), sea},
		{"east border", 0, image.Pt(555, 382), land},
		{"east of the border", 0, image.Pt(556, 382), sea},
		{"north coast", 0, image.Pt(302, 40), land},
		{"sea north of the coast", 0, image.Pt(302, 39), sea},
		{"south border", 0, image.Pt(302, 725), land},
		{"south of the border", 0, image.Pt(302, 726), sea},
		{"label top left", 0, knmiRadarLabel.Min, label},
		{"label bottom right", 0, knmiRadarLabel.Max.Sub(image.Pt(1, 1)), label},
		{"beside the label", 0, image.Pt(knmiRadarLabel.Max.X, knmiRadarLabel.Min.Y), sea},
		{"second frame's rain", 1, image.Pt(302, 382), rain},
		{"first frame kept under the patch", 1, image.Pt(60, 382), land},
		{"label kept in the second frame", 1, knmiRadarLabel.Min, label},
	} {
		got := color.RGBAModel.Convert(centred.Image[tc.frame].At(tc.at.X, tc.at.Y)).(color.RGBA)
		if got != tc.want {
			t.Errorf("%s: frame %d at %v = %v, want %v", tc.name, tc.frame, tc.at, got, tc.want)
		}
	}

	other := image.NewPaletted(image.Rect(0, 0, 425, 445), palette)
	var unchanged bytes.Buffer
	if err := gif.EncodeAll(&unchanged, &gif.GIF{Image: []*image.Paletted{other}, Delay: []int{50}}); err != nil {
		t.Fatal(err)
	}
	if body, err := centreKNMIRadarMap(unchanged.Bytes()); err != nil || !bytes.Equal(body, unchanged.Bytes()) {
		t.Fatalf("425x445 map changed (err %v)", err)
	}
}

// knmiCroppedFrame draws the cropped KNMI map with the time label, a border
// line, a rain pixel and temperature circles at the first stations.
func knmiCroppedFrame(temperatures int) *image.RGBA {
	frame := image.NewRGBA(knmiRadarCropBounds)
	draw.Draw(frame, frame.Bounds(), image.NewUniform(knmiRadarSea), image.Point{}, draw.Src)
	draw.Draw(frame, image.Rect(50, 40, 556, 726), image.NewUniform(knmiRadarLand), image.Point{}, draw.Src)
	frame.SetRGBA(150, 300, knmiRadarBorder)
	frame.SetRGBA(300, 600, color.RGBA{28, 168, 243, 255})
	draw.Draw(frame, knmiRadarLabel, image.NewUniform(knmiRadarLabelFill), image.Point{}, draw.Src)
	frame.SetRGBA(40, 40, knmiRadarLabelText)
	for _, station := range knmiRadarStations[:temperatures] {
		for y := station.Y - knmiRadarStationRadius; y <= station.Y+knmiRadarStationRadius; y++ {
			for x := station.X - knmiRadarStationRadius; x <= station.X+knmiRadarStationRadius; x++ {
				if inRadarCircle(image.Pt(x, y), station, knmiRadarStationRadius) {
					frame.SetRGBA(x, y, color.RGBA{115, 124, 134, 255})
				}
			}
		}
		digits := image.Rect(station.X-4, station.Y-3, station.X+4, station.Y+3)
		draw.Draw(frame, digits, image.NewUniform(color.White), image.Point{}, draw.Src)
	}
	return frame
}

func TestKNMIRadarObservations(t *testing.T) {
	for _, tc := range []struct {
		temperatures int
		want         radarFrameKind
	}{
		{6, radarObserved},
		{3, radarObserved},
		{2, radarUnknown},
		{0, radarPredicted},
	} {
		if got := radarObservationKind(knmiCroppedFrame(tc.temperatures)); got != tc.want {
			t.Errorf("%d temperatures: kind=%v, want %v", tc.temperatures, got, tc.want)
		}
	}
}
