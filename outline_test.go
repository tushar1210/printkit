package printkit

import "testing"

func TestNestMarks(t *testing.T) {
	got := nestMarks([]domMark{
		{level: 1, title: "A", page: 1},
		{level: 2, title: "A.1", page: 2},
		{level: 3, title: "A.1.a", page: 2},
		{level: 2, title: "A.2", page: 3},
		{level: 1, title: "B", page: 4},
	})
	if len(got) != 2 || got[0].Title != "A" || got[1].Title != "B" || got[1].PageFrom != 4 {
		t.Fatalf("roots %#v", got)
	}
	if len(got[0].Kids) != 2 || got[0].Kids[0].Title != "A.1" || len(got[0].Kids[0].Kids) != 1 {
		t.Fatalf("kids %#v", got[0].Kids)
	}
	if got[0].Kids[0].Kids[0].Title != "A.1.a" || got[0].Kids[1].PageFrom != 3 {
		t.Fatalf("nested %#v", got[0].Kids)
	}
}

func TestRequestNeedsPost(t *testing.T) {
	plain := Request{Inputs: []Input{FromHTML("<p>x</p>")}}
	if requestNeedsPost(plain) {
		t.Fatal("default single input already has its outline from the print")
	}
	off := plain
	off.Outline.Enabled = boolPtr(false)
	if requestNeedsPost(off) {
		t.Fatal("outline off and untagged has nothing to post-process")
	}
	if !canDirectWrite(off) {
		t.Fatal("outline off should stream")
	}
	if canDirectWrite(plain) {
		t.Fatal("default outline must not take the direct path")
	}
	ranged := plain
	ranged.PageRanges = "1-2"
	if !requestNeedsPost(ranged) || canDirectWrite(ranged) {
		t.Fatal("page ranges need a pass")
	}
	meta := plain
	meta.Metadata.Title = "T"
	if !requestNeedsPost(meta) || canDirectWrite(meta) {
		t.Fatal("metadata needs a pass")
	}
	covered := plain
	covered.Cover = &Input{HTML: "<p>c</p>"}
	if !requestNeedsPost(covered) || canDirectWrite(covered) {
		t.Fatal("cover needs a merge")
	}
	taggedOff := off
	taggedOff.Tagged = true
	if !requestNeedsPost(taggedOff) {
		t.Fatal("tagged output with the outline disabled may still contain /Outlines")
	}
}
