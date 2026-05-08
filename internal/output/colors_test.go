package output

import "testing"

func TestSupportsTrueColorEnv(t *testing.T) {
	if !supportsTrueColorEnv(fakeEnv(map[string]string{"COLORTERM": "truecolor"})) {
		t.Fatal("COLORTERM=truecolor should enable truecolor")
	}
	if !supportsTrueColorEnv(fakeEnv(map[string]string{"TERM": "xterm-24bit"})) {
		t.Fatal("TERM containing 24bit should enable truecolor")
	}
	if supportsTrueColorEnv(fakeEnv(map[string]string{"LAEVITAS_COLOR": "ansi", "COLORTERM": "truecolor"})) {
		t.Fatal("LAEVITAS_COLOR=ansi should force conservative palette")
	}
}

func TestDetectColorPaletteFallbackUsesBrightAnsi(t *testing.T) {
	p := detectColorPaletteWithEnv(fakeEnv(nil))
	if p.BrandGreen != "\033[92m" {
		t.Fatalf("fallback BrandGreen = %q, want bright green", p.BrandGreen)
	}
	if p.Red != "\033[91m" {
		t.Fatalf("fallback Red = %q, want bright red", p.Red)
	}
	if p.BrandGreyMid != "\033[90m" {
		t.Fatalf("fallback BrandGreyMid = %q, want bright black/grey", p.BrandGreyMid)
	}
}

func fakeEnv(values map[string]string) func(string) string {
	return func(key string) string {
		if values == nil {
			return ""
		}
		return values[key]
	}
}
