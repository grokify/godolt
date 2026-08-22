package godolt

import "testing"

func TestLocalDSN(t *testing.T) {
	if got := LocalDSN(13307, "omniroadmap"); got != "root:@tcp(127.0.0.1:13307)/omniroadmap" {
		t.Fatalf("LocalDSN = %q", got)
	}
}

func TestEnsureParseTime(t *testing.T) {
	tests := []struct{ in, want string }{
		{"root:@tcp(127.0.0.1:13307)/db", "root:@tcp(127.0.0.1:13307)/db?parseTime=true"},
		{"root:@tcp(127.0.0.1:13307)/db?charset=utf8", "root:@tcp(127.0.0.1:13307)/db?charset=utf8&parseTime=true"},
		{"root:@tcp(127.0.0.1:13307)/db?parseTime=true", "root:@tcp(127.0.0.1:13307)/db?parseTime=true"},
		{"root:@tcp(127.0.0.1:13307)/db?parseTime=false", "root:@tcp(127.0.0.1:13307)/db?parseTime=false"},
	}
	for _, tt := range tests {
		if got := EnsureParseTime(tt.in); got != tt.want {
			t.Errorf("EnsureParseTime(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitDSN(t *testing.T) {
	base, db, err := SplitDSN("root:@tcp(127.0.0.1:13307)/omniroadmap?parseTime=true")
	if err != nil {
		t.Fatal(err)
	}
	if base != "root:@tcp(127.0.0.1:13307)/?parseTime=true" || db != "omniroadmap" {
		t.Fatalf("SplitDSN = (%q, %q)", base, db)
	}

	if _, _, err := SplitDSN("no-database-segment"); err == nil {
		t.Fatal("expected error for DSN without database segment")
	}
	if _, _, err := SplitDSN("root:@tcp(127.0.0.1:13307)/"); err == nil {
		t.Fatal("expected error for empty database name")
	}
	if _, _, err := SplitDSN("root:@tcp(127.0.0.1:13307)/db%zz"); err == nil {
		t.Fatal("expected error for invalid escape in database name")
	}
}
