package main

import (
	"fmt"
	"testing"
)

func TestProfileHeaders(t *testing.T) {
	c := NewClient(Config{BearerToken: "test"})
	fmt.Printf("len(profileHeaders) = %d\n", len(c.profileHeaders))
	for k, v := range c.profileHeaders {
		fmt.Printf("  %q: %v\n", k, v)
	}
	if len(c.profileHeaders) < 5 {
		t.Errorf("profileHeaders too few: %d", len(c.profileHeaders))
	}
	if _, ok := c.profileHeaders["sec-ch-ua"]; !ok {
		t.Error("sec-ch-ua missing from profileHeaders")
	}
}
