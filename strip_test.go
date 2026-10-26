package printkit

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func TestStripStructureFastDropsTreeKeepsOutline(t *testing.T) {
	padding := strings.Repeat("PADDING", 20_000)
	objs := []string{
		`<< /Title (Chapter \(2 0 R\)) /Creator (Mozilla/5.0 \(X11\)) /Producer (Skia/PDF) >>`,
		`<< /Type /Catalog /Pages 3 0 R /MarkInfo << /Type /MarkInfo /Marked true >> /StructTreeRoot 8 0 R /Outlines 6 0 R /Lang (en) >>`,
		`<< /Type /Pages /Count 1 /Kids [4 0 R] >>`,
		`<< /Type /Page /Parent 3 0 R /MediaBox [0 0 200 200] /Contents 5 0 R /Resources << /ProcSet [/PDF /Text] >> /StructParents 0 >>`,
		"<< /Length 11 0 R >>\nstream\nq\nQ\nendstream",
		`<< /Type /Outlines /First 7 0 R /Last 7 0 R /Count 1 >>`,
		`<< /Title (Chapter \(2 0 R\)) /Dest [4 0 R /XYZ 0 200 0] /Parent 6 0 R /SE 9 0 R /S#45 10 0 R >>`,
		`<< /Type /StructTreeRoot /K 9 0 R >>`,
		`<< /Type /StructElem /S /H1 /P 8 0 R /Pg 4 0 R /K 10 0 R >>`,
		`<< /Length 7 >>` + "\nstream\n" + padding[:7] + "\nendstream",
		`3`,
	}
	// Object 10 is the big unreachable blob. Object 11 is the indirect length (3).
	objs[9] = "<< /Reason (tree only) >>\n" + padding
	in := buildClassicPDF(t, objs, 2, 1)
	if len(in) < len(padding) {
		t.Fatalf("fixture did not embed the structure blob: %d", len(in))
	}

	start := time.Now()
	out, err := stripStructureFast(in)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	viaTree, err := stripStructureTree(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, viaTree) {
		t.Fatal("stripStructureTree did not use the fast path")
	}
	if len(out) >= len(in)/5 {
		t.Fatalf("size did not drop: in %d out %d", len(in), len(out))
	}
	if bytes.Contains(out, []byte("PADDING")) || bytes.Contains(out, []byte("/StructTreeRoot")) ||
		bytes.Contains(out, []byte("/StructElem")) || bytes.Contains(out, []byte("/MarkInfo")) ||
		bytes.Contains(out, []byte("/SE")) {
		t.Fatal("structure tree survived")
	}
	if !bytes.Contains(out, []byte("q\nQ\n")) {
		t.Fatal("content stream was not copied")
	}
	if elapsed > time.Second {
		t.Fatalf("fast strip took %s", elapsed)
	}
	if n, err := api.PageCount(context.Background(), bytes.NewReader(out), nil); err != nil || n != 1 {
		t.Fatalf("pages: %v %d", err, n)
	}
	if err := api.Validate(context.Background(), bytes.NewReader(out), nil, nil); err != nil {
		t.Fatal(err)
	}
	marks, err := pdfBookmarks(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 1 || marks[0].Title != "Chapter (2 0 R)" || marks[0].PageFrom != 1 {
		t.Fatalf("outline: %+v", marks)
	}
}

func TestStripStructureFastIndirectLengthAndPassthrough(t *testing.T) {
	plain := []byte("%PDF-1.4\nno structure here\n")
	out, err := stripStructureTree(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("PDF without a structure tree was rewritten")
	}
	if _, err := stripStructureFast([]byte("%PDF-1.4\n/StructTreeRoot")); err == nil {
		t.Fatal("truncated PDF should not take the fast path")
	}
}

func buildClassicPDF(t *testing.T, objs []string, root, info int) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<</Size %d/Root %d 0 R/Info %d 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, root, info, xref)
	return buf.Bytes()
}
