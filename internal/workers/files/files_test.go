package files

import "testing"

func TestError(t *testing.T) {
	c := Client{Worker: "asr"}
	for body, want := range map[string]string{
		`{"detail":"cesta mimo kořen"}`:       "worker asr: HTTP 400: cesta mimo kořen",
		`{"detail":[{"loc":["body"],"x":1}]}`: `worker asr: HTTP 400: [{"loc":["body"],"x":1}]`,
		`Internal Server Error`:               "worker asr: HTTP 400: Internal Server Error",
	} {
		if got := c.Error(400, []byte(body)).Error(); got != want {
			t.Errorf("%s: %q", body, got)
		}
	}
}
