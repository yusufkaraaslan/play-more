package models

import (
	"reflect"
	"testing"
)

func TestMergeFeaturedIDs(t *testing.T) {
	tests := []struct {
		name     string
		pinned   []string
		trending []string
		newest   []string
		limit    int
		want     []string
	}{
		{
			name:     "pins first then alternate trending/newest",
			pinned:   []string{"p1", "p2"},
			trending: []string{"t1", "t2"},
			newest:   []string{"n1", "n2"},
			limit:    6,
			want:     []string{"p1", "p2", "t1", "n1", "t2", "n2"},
		},
		{
			name:     "dedup across sources",
			pinned:   []string{"a"},
			trending: []string{"a", "t1"},
			newest:   []string{"t1", "n1"},
			limit:    6,
			want:     []string{"a", "t1", "n1"},
		},
		{
			name:     "limit truncates",
			pinned:   []string{"p1"},
			trending: []string{"t1", "t2", "t3"},
			newest:   []string{"n1", "n2"},
			limit:    3,
			want:     []string{"p1", "t1", "n1"},
		},
		{
			name:     "trending exhausted falls back to newest",
			pinned:   nil,
			trending: []string{"t1"},
			newest:   []string{"n1", "n2", "n3"},
			limit:    4,
			want:     []string{"t1", "n1", "n2", "n3"},
		},
		{
			name:     "empty everything returns empty slice",
			pinned:   nil,
			trending: nil,
			newest:   nil,
			limit:    6,
			want:     []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeFeaturedIDs(tt.pinned, tt.trending, tt.newest, tt.limit)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
