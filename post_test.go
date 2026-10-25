package printkit

import (
	"errors"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
)

func TestParsePageRanges(t *testing.T) {
	got, err := parsePageRanges("1-3,5", 6)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 2, 3, 5}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
	got, err = parsePageRanges("-2", 4)
	if err != nil || len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("%v %v", got, err)
	}
	got, err = parsePageRanges("2-", 3)
	if err != nil || len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := parsePageRanges("0", 3); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("zero: %v", err)
	}
	if _, err := parsePageRanges("9", 3); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("past end: %v", err)
	}
	if _, err := parsePageRanges("2-1", 3); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("backwards: %v", err)
	}
}

func TestBookmarkOps(t *testing.T) {
	tree := []pdfcpu.Bookmark{
		{Title: "A", PageFrom: 1, Kids: []pdfcpu.Bookmark{{Title: "A.1", PageFrom: 2}}},
		{Title: "B", PageFrom: 4},
	}
	shifted := shiftBookmarks(tree, 3)
	if shifted[0].PageFrom != 4 || shifted[0].Kids[0].PageFrom != 5 || shifted[1].PageFrom != 7 {
		t.Fatalf("shift %#v", shifted)
	}
	if tree[0].PageFrom != 1 {
		t.Fatal("shift mutated the input")
	}
	filtered := filterDepth(tree, 1)
	if len(filtered) != 2 || len(filtered[0].Kids) != 0 {
		t.Fatalf("depth %#v", filtered)
	}
	mapped := remapBookmarks(shifted, map[int]int{4: 1, 7: 2})
	if len(mapped) != 2 || mapped[0].PageFrom != 1 || len(mapped[0].Kids) != 0 || mapped[1].PageFrom != 2 {
		t.Fatalf("remap %#v", mapped)
	}
	dropped := remapBookmarks(shifted, map[int]int{7: 1})
	if len(dropped) != 1 || dropped[0].Title != "B" || dropped[0].PageFrom != 1 {
		t.Fatalf("drop %#v", dropped)
	}
	labels := sectionLabels(tree, 4)
	if labels[0].section != "A" || labels[1].subsection != "A.1" || labels[3].section != "B" || labels[3].subsection != "" {
		t.Fatalf("labels %#v", labels)
	}
	groups := concatBookmarks([][]pdfcpu.Bookmark{{{Title: "A", PageFrom: 1}}, {{Title: "B", PageFrom: 1}}}, []int{2, 2})
	if groups[0].PageFrom != 1 || groups[1].PageFrom != 3 {
		t.Fatalf("concat %#v", groups)
	}
}

func TestLoadAndOptionErrors(t *testing.T) {
	opt := optionf("Page.Size", "unknown paper size %q", "Poster")
	var oe *OptionError
	if !errors.As(opt, &oe) || oe.Field != "Page.Size" || !errors.Is(opt, ErrInvalidOption) {
		t.Fatalf("%v", opt)
	}
	load := &LoadError{URL: "https://example.test", Status: 404, Detail: "missing"}
	if !errors.Is(load, ErrLoadFailed) || load.Error() == "" {
		t.Fatal(load)
	}
}
