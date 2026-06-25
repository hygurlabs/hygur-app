package push

import "testing"

func TestValidEndpoint(t *testing.T) {
	good := []string{
		"https://fcm.googleapis.com/fcm/send/abc123",
		"https://updates.push.services.mozilla.com/wpush/v2/xxx",
		"https://wns2-par02p.notify.windows.com/w/?token=xxx",
		"https://web.push.apple.com/QABC",
	}
	for _, e := range good {
		if !ValidEndpoint(e) {
			t.Errorf("expected valid push endpoint: %q", e)
		}
	}

	bad := []string{
		"http://fcm.googleapis.com/x",            // not https
		"https://169.254.169.254/latest/meta-data", // cloud metadata (SSRF)
		"https://localhost/x",
		"https://10.0.0.5/internal",
		"https://hygur-server.tenant.svc/x", // internal k8s service
		"https://evil.com/x",
		"https://fcm.googleapis.com.evil.com/x", // suffix confusion
		"https://notgoogleapis.com/x",           // suffix confusion (no dot boundary)
		"",
		"not a url",
	}
	for _, e := range bad {
		if ValidEndpoint(e) {
			t.Errorf("expected REJECTED endpoint: %q", e)
		}
	}
}
