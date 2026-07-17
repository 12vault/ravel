package main

import "testing"

func TestAutoRefreshDisabled(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", " yes "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("RAVEL_NO_AUTO_REFRESH", value)
			if !autoRefreshDisabled() {
				t.Fatalf("RAVEL_NO_AUTO_REFRESH=%q did not disable refresh", value)
			}
		})
	}
	t.Setenv("RAVEL_NO_AUTO_REFRESH", "0")
	if autoRefreshDisabled() {
		t.Fatal("RAVEL_NO_AUTO_REFRESH=0 disabled refresh")
	}
}
