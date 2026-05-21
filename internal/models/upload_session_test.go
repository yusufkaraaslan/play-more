package models

import (
	"reflect"
	"testing"
)

func TestAddRange_emptyStart(t *testing.T) {
	got := AddRange(nil, 0, 100)
	want := [][2]int64{{0, 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_appendsContiguous(t *testing.T) {
	got := AddRange([][2]int64{{0, 100}}, 100, 200)
	want := [][2]int64{{0, 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_appendsDisjoint(t *testing.T) {
	got := AddRange([][2]int64{{0, 100}}, 200, 300)
	want := [][2]int64{{0, 100}, {200, 300}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_fillsGap(t *testing.T) {
	got := AddRange([][2]int64{{0, 100}, {200, 300}}, 100, 200)
	want := [][2]int64{{0, 300}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_overlapMerges(t *testing.T) {
	got := AddRange([][2]int64{{0, 100}}, 50, 150)
	want := [][2]int64{{0, 150}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_fullyContained(t *testing.T) {
	got := AddRange([][2]int64{{0, 200}}, 50, 100)
	want := [][2]int64{{0, 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAddRange_outOfOrderInsert(t *testing.T) {
	got := AddRange([][2]int64{{200, 300}}, 0, 100)
	want := [][2]int64{{0, 100}, {200, 300}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestIsComplete_singleFullRange(t *testing.T) {
	if !IsComplete([][2]int64{{0, 500}}, 500) {
		t.Fatal("expected complete")
	}
}

func TestIsComplete_gap(t *testing.T) {
	if IsComplete([][2]int64{{0, 100}, {200, 500}}, 500) {
		t.Fatal("expected incomplete (gap)")
	}
}

func TestIsComplete_short(t *testing.T) {
	if IsComplete([][2]int64{{0, 499}}, 500) {
		t.Fatal("expected incomplete (short)")
	}
}

func TestIsComplete_empty(t *testing.T) {
	if IsComplete(nil, 500) {
		t.Fatal("expected incomplete (empty)")
	}
}

func TestMissingRanges_oneGap(t *testing.T) {
	got := MissingRanges([][2]int64{{0, 100}, {200, 500}}, 500)
	want := [][2]int64{{100, 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMissingRanges_trailing(t *testing.T) {
	got := MissingRanges([][2]int64{{0, 100}}, 500)
	want := [][2]int64{{100, 500}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMissingRanges_leading(t *testing.T) {
	got := MissingRanges([][2]int64{{100, 500}}, 500)
	want := [][2]int64{{0, 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMissingRanges_complete(t *testing.T) {
	got := MissingRanges([][2]int64{{0, 500}}, 500)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestMissingRanges_empty(t *testing.T) {
	got := MissingRanges(nil, 500)
	want := [][2]int64{{0, 500}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestReceivedBytes_contiguousFromZero(t *testing.T) {
	if got := ReceivedBytes([][2]int64{{0, 100}, {200, 300}}); got != 100 {
		t.Fatalf("got %d want 100", got)
	}
}

func TestReceivedBytes_emptyOrGapAtStart(t *testing.T) {
	if got := ReceivedBytes(nil); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
	if got := ReceivedBytes([][2]int64{{50, 100}}); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
}
