package transport

import "testing"

func TestParseSemVer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in    string
		want  SemVer
		isErr bool
	}{
		{"0.121.0", SemVer{0, 121, 0}, false},
		{"codex 0.121.0", SemVer{0, 121, 0}, false},
		{"v1.2.3", SemVer{1, 2, 3}, false},
		{"2.0.0-alpha+build.99", SemVer{2, 0, 0}, false},
		{"notaversion", SemVer{}, true},
		{"", SemVer{}, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSemVer(tt.in)
			if (err != nil) != tt.isErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.isErr)
			}
			if !tt.isErr && got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSemVer_AtLeast(t *testing.T) {
	t.Parallel()
	tests := []struct {
		v, req SemVer
		want   bool
	}{
		{SemVer{0, 121, 0}, SemVer{0, 121, 0}, true},
		{SemVer{0, 121, 1}, SemVer{0, 121, 0}, true},
		{SemVer{0, 122, 0}, SemVer{0, 121, 0}, true},
		{SemVer{1, 0, 0}, SemVer{0, 121, 0}, true},
		{SemVer{0, 120, 9}, SemVer{0, 121, 0}, false},
		{SemVer{0, 121, 0}, SemVer{0, 121, 1}, false},
		{SemVer{0, 121, 0}, SemVer{1, 0, 0}, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.v.String()+"vs"+tt.req.String(), func(t *testing.T) {
			t.Parallel()
			if got := tt.v.AtLeast(tt.req); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
