package geo

import "testing"

func TestParsearAzimut(t *testing.T) {
	tests := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		{"76°E", 76, true},
		{"34°N", 34, true},
		{"321°NW", 321, true},
		{"248", 248, true},
		{"  230.5  ", 230.5, true},
		{"", 0, false},
		{"   ", 0, false},
		{"sin datos", 0, false},
	}
	for _, tc := range tests {
		got, ok := ParsearAzimut(tc.in)
		if ok != tc.wantOK {
			t.Fatalf("ParsearAzimut(%q) ok=%v, want %v", tc.in, ok, tc.wantOK)
		}
		if ok && got != tc.want {
			t.Fatalf("ParsearAzimut(%q)=%v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestObtenerApertura(t *testing.T) {
	if got := ObtenerApertura("Ubiquiti Wave AP"); got != 30 {
		t.Fatalf("Wave: got %v", got)
	}
	if got := ObtenerApertura("WABE-60"); got != 30 {
		t.Fatalf("Wabe: got %v", got)
	}
	if got := ObtenerApertura("AirMax AC"); got != 90 {
		t.Fatalf("default: got %v", got)
	}
}
