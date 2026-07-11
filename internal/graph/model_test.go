package graph

import "testing"

func TestExternalFunctionIDUsesImportPathAndSelector(t *testing.T) {
	want := "go-external-function://example.com/acme/log#Write"
	if got := ExternalFunctionID("example.com/acme/log", "Write"); got != want {
		t.Fatalf("ExternalFunctionID() = %q, want %q", got, want)
	}
	if ExternalFunctionID("example.com/acme/log", "Write") == ExternalFunctionID("example.com/acme/log", "Read") {
		t.Fatal("different selectors produced the same external function ID")
	}
	if ExternalFunctionID("example.com/acme/log", "Write") == ExternalFunctionID("example.com/other/log", "Write") {
		t.Fatal("different import paths produced the same external function ID")
	}
}

func TestUnresolvedCallSiteIDDoesNotCollapseSameNamedCalls(t *testing.T) {
	first := UnresolvedCallSiteID("go://pkg.First", "pkg/calls.go", "value.Missing", 3, 25)
	if got := UnresolvedCallSiteID("go://pkg.First", "pkg/calls.go", "value.Missing", 3, 25); got != first {
		t.Fatalf("same callsite ID changed: %q != %q", got, first)
	}
	cases := []string{
		UnresolvedCallSiteID("go://pkg.Second", "pkg/calls.go", "value.Missing", 3, 25),
		UnresolvedCallSiteID("go://pkg.First", "pkg/other.go", "value.Missing", 3, 25),
		UnresolvedCallSiteID("go://pkg.First", "pkg/calls.go", "value.Missing", 4, 25),
		UnresolvedCallSiteID("go://pkg.First", "pkg/calls.go", "value.Missing", 3, 26),
	}
	for _, candidate := range cases {
		if candidate == first {
			t.Fatalf("distinct callsite collapsed into %q", first)
		}
	}
}
