import java.time.Instant;
import java.time.Duration;
import net.surfly.weather.widget.net.RadarPolicy;

class RadarPolicyChecks {
    private static void check(boolean value, String message) {
        if (!value) throw new AssertionError(message);
    }

    public static void main(String[] args) {
        var policy = RadarPolicy.INSTANCE;
        var now = Instant.parse("2026-10-10T12:00:00Z");
        check(policy.isFresh(now.minus(Duration.ofMinutes(5)), now), "Recent observation rejected");
        check(!policy.isFresh(now.minus(Duration.ofMinutes(21)), now), "Stale observation accepted");
        check(!policy.isFresh(now.plus(Duration.ofMinutes(3)), now), "Future frame accepted");
        check(policy.isFresh(now.plus(Duration.ofMinutes(1)), now), "Small clock skew rejected");
        check(policy.fits(406, 215, 0.9f), "Phone widget should fit");
        check(!policy.fits(339, 215, 1f), "Narrow widget should retain its layout");
        check(!policy.fits(406, 209, 1f), "Short widget should retain its layout");
        check(!policy.fits(406, 215, 1.3f), "Large fonts should retain their layout");
        System.out.println("Radar freshness and widget sizing checks passed.");
    }
}
