package main

import (
	"fmt"
	"testing"
)

func TestBloom(t *testing.T) {
	b := newBloom(10000)
	for i := 0; i < 10000; i++ {
		b.addHash(hash(fmt.Sprint(i)))
	}
	for i := 0; i < 10000; i++ {
		if !b.contains(fmt.Sprint(i)) {
			t.Fatalf("false negative %d", i)
		}
	}
	falsePositives := 0
	for i := 10000; i < 20000; i++ {
		if b.contains(fmt.Sprint(i)) {
			falsePositives++
		}
	}
	if falsePositives > 300 {
		t.Fatalf("false positive rate %f", float64(falsePositives)/10000)
	}
}
