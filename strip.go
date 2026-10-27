package printkit

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// stripStructureFast rewrites a classic (non-stream) xref PDF, keeping every
// object reachable from the catalog and the info dictionary except the tagged
// structure tree. Chrome hangs that tree off the catalog (/StructTreeRoot,
// /MarkInfo) and off each outline item (/SE). Following /SE retains tens of
// thousands of structure elements, which is the bulk of a large table PDF.
//
// The rewriter returns an error for anything it does not fully understand
// (xref streams, object streams, encryption, a non-zero generation). Callers
// fall back to pdfcpu in that case.
func stripStructureFast(b []byte) ([]byte, error) {
	if len(b) < 8 || !bytes.HasPrefix(b, []byte("%PDF-")) {
		return nil, errFast("header")
	}
	start, err := findStartXref(b)
	if err != nil {
		return nil, err
	}
	xref, root, info, err := loadXref(b, start)
	if err != nil {
		return nil, err
	}
	objs := make(map[int]pdfObj, 64)
	var walk func(int) error
	walk = func(n int) error {
		if n <= 0 {
			return errFast("bad ref")
		}
		if _, ok := objs[n]; ok {
			return nil
		}
		off, ok := xref[n]
		if !ok {
			return errFast("missing object")
		}
		obj, err := readObj(b, xref, off, n)
		if err != nil {
			return err
		}
		if isStructureObject(obj.raw) {
			return errFast("structure object")
		}
		objs[n] = obj
		for _, r := range obj.refs {
			if err := walk(r); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	if !bytes.Contains(objs[root].raw, []byte("/StructTreeRoot")) {
		// The byte string lived in a stream, not the catalog. Leave the file.
		return b, nil
	}
	if info != 0 {
		if err := walk(info); err != nil {
			return nil, err
		}
	}
	return writeKept(b, objs, root, info)
}

func isStructureObject(raw []byte) bool {
	return bytes.Contains(raw, []byte("/StructElem")) ||
		bytes.Contains(raw, []byte("/Type /StructTreeRoot")) ||
		bytes.Contains(raw, []byte("/Type/StructTreeRoot"))
}

func errFast(msg string) error {
	return fmt.Errorf("printkit: strip fast: %s", msg)
}

func pdfVersion(b []byte) string {
	rest := b[len("%PDF-"):]
	i := 0
	for i < len(rest) && i < 8 && rest[i] != '\n' && rest[i] != '\r' && rest[i] != ' ' {
		i++
	}
	if i == 0 {
		return "1.4"
	}
	return string(rest[:i])
}

func findStartXref(b []byte) (int, error) {
	const window = 4096
	from := 0
	if len(b) > window {
		from = len(b) - window
	}
	rel := bytes.LastIndex(b[from:], []byte("startxref"))
	if rel < 0 {
		return 0, errFast("startxref")
	}
	s := pdfScan{b: b, i: from + rel + len("startxref")}
	s.skipWS()
	isInt, off, err := s.readNumber()
	if err != nil || !isInt || off < 0 {
		return 0, errFast("startxref")
	}
	return off, nil
}

func loadXref(b []byte, off int) (map[int]int, int, int, error) {
	combined := map[int]int{}
	blocked := map[int]bool{}
	seen := map[int]bool{}
	var root, info int
	for hop := 0; hop < 8; hop++ {
		if seen[off] {
			return nil, 0, 0, errFast("xref cycle")
		}
		seen[off] = true
		objs, freed, trailer, err := parseXrefAt(b, off)
		if err != nil {
			return nil, 0, 0, err
		}
		if hop == 0 {
			root = trailer.root
			info = trailer.info
		}
		for id, o := range objs {
			if blocked[id] {
				continue
			}
			if _, ok := combined[id]; ok {
				continue
			}
			combined[id] = o
		}
		for id := range freed {
			if _, ok := combined[id]; !ok {
				blocked[id] = true
			}
		}
		if trailer.prev == 0 {
			return combined, root, info, nil
		}
		off = trailer.prev
	}
	return nil, 0, 0, errFast("xref chain")
}

type xrefTrailer struct {
	root, info, prev int
}

func parseXrefAt(b []byte, off int) (map[int]int, map[int]bool, xrefTrailer, error) {
	if off < 0 || off >= len(b) {
		return nil, nil, xrefTrailer{}, errFast("startxref")
	}
	s := pdfScan{b: b, i: off}
	s.skipWS()
	if !s.consumeKeyword("xref") {
		return nil, nil, xrefTrailer{}, errFast("xref stream")
	}
	objs := map[int]int{}
	freed := map[int]bool{}
	for {
		s.skipWS()
		if s.consumeKeyword("trailer") {
			break
		}
		isInt, start, err := s.readNumber()
		if err != nil || !isInt {
			return nil, nil, xrefTrailer{}, errFast("xref subsection")
		}
		s.skipWS()
		isInt, count, err := s.readNumber()
		if err != nil || !isInt || count < 0 || count > 2_000_000 {
			return nil, nil, xrefTrailer{}, errFast("xref count")
		}
		for j := 0; j < count; j++ {
			s.skipWS()
			_, objOff, err := s.readNumber()
			if err != nil {
				return nil, nil, xrefTrailer{}, errFast("xref offset")
			}
			s.skipWS()
			isInt, gen, err := s.readNumber()
			if err != nil || !isInt {
				return nil, nil, xrefTrailer{}, errFast("xref gen")
			}
			s.skipWS()
			if s.i >= len(b) {
				return nil, nil, xrefTrailer{}, errFast("xref type")
			}
			typ := b[s.i]
			s.i++
			id := start + j
			switch typ {
			case 'n':
				if gen != 0 {
					return nil, nil, xrefTrailer{}, errFast("generation")
				}
				objs[id] = objOff
				delete(freed, id)
			case 'f':
				delete(objs, id)
				freed[id] = true
			default:
				return nil, nil, xrefTrailer{}, errFast("xref type")
			}
		}
	}
	st := &scanState{trailer: true}
	if err := s.scanValue(nil, st, 0); err != nil {
		return nil, nil, xrefTrailer{}, err
	}
	if st.encrypt || st.xrefStm {
		return nil, nil, xrefTrailer{}, errFast("unsupported trailer")
	}
	if st.root == 0 {
		return nil, nil, xrefTrailer{}, errFast("root")
	}
	return objs, freed, xrefTrailer{root: st.root, info: st.info, prev: st.prev}, nil
}

type pdfObj struct {
	raw    []byte
	stream []byte
	refs   []int
}

func readObj(b []byte, xref map[int]int, off, want int) (pdfObj, error) {
	s := pdfScan{b: b, i: off}
	num, gen, err := s.readHeader()
	if err != nil {
		return pdfObj{}, err
	}
	if num != want || gen != 0 {
		return pdfObj{}, errFast("object id")
	}
	s.skipWS()
	var refs []int
	st := &scanState{captureLength: true, refs: &refs}
	start := s.i
	if err := s.scanValue(nil, st, 0); err != nil {
		return pdfObj{}, err
	}
	raw := b[start:s.i]
	s.skipWS()
	var stream []byte
	if s.consumeKeyword("stream") {
		length, ok, err := streamLength(b, xref, st)
		if err != nil || !ok {
			return pdfObj{}, errFast("length")
		}
		if !s.consumeEOL() {
			return pdfObj{}, errFast("stream eol")
		}
		if length < 0 || length > len(b)-s.i {
			return pdfObj{}, errFast("stream length")
		}
		stream = b[s.i : s.i+length]
		s.i += length
		s.consumeEOL()
		if !s.consumeKeyword("endstream") {
			return pdfObj{}, errFast("endstream")
		}
	}
	s.skipWS()
	if !s.consumeKeyword("endobj") {
		return pdfObj{}, errFast("endobj")
	}
	return pdfObj{raw: raw, stream: stream, refs: refs}, nil
}

func streamLength(b []byte, xref map[int]int, st *scanState) (int, bool, error) {
	if st.lengthRef != 0 {
		n, err := bareInt(b, xref, st.lengthRef)
		if err != nil {
			return 0, false, err
		}
		return n, true, nil
	}
	if st.lengthSet {
		return st.length, true, nil
	}
	return 0, false, nil
}

func bareInt(b []byte, xref map[int]int, n int) (int, error) {
	off, ok := xref[n]
	if !ok {
		return 0, errFast("length ref")
	}
	s := pdfScan{b: b, i: off}
	num, gen, err := s.readHeader()
	if err != nil || num != n || gen != 0 {
		return 0, errFast("length ref")
	}
	s.skipWS()
	isInt, v, err := s.readNumber()
	if err != nil || !isInt {
		return 0, errFast("length ref")
	}
	s.skipWS()
	if !s.consumeKeyword("endobj") {
		return 0, errFast("length ref")
	}
	return v, nil
}

func writeKept(b []byte, objs map[int]pdfObj, root, info int) ([]byte, error) {
	if _, ok := objs[root]; !ok {
		return nil, errFast("root")
	}
	ids := make([]int, 0, len(objs))
	for id := range objs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	renumber := make(map[int]int, len(ids))
	for i, id := range ids {
		renumber[id] = i + 1
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-")
	buf.WriteString(pdfVersion(b))
	buf.WriteByte('\n')
	buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})
	offsets := make([]int, len(ids)+1)
	for _, id := range ids {
		newN := renumber[id]
		offsets[newN] = buf.Len()
		buf.Write(strconv.AppendInt(nil, int64(newN), 10))
		buf.WriteString(" 0 obj\n")
		rewritten, err := rewriteValue(objs[id].raw, renumber)
		if err != nil {
			return nil, err
		}
		buf.Write(rewritten)
		buf.WriteByte('\n')
		if objs[id].stream != nil {
			buf.WriteString("stream\n")
			buf.Write(objs[id].stream)
			buf.WriteString("\nendstream\n")
		}
		buf.WriteString("endobj\n")
	}
	xrefAt := buf.Len()
	buf.WriteString("xref\n0 ")
	buf.Write(strconv.AppendInt(nil, int64(len(ids)+1), 10))
	buf.WriteByte('\n')
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(ids); i++ {
		// Each xref entry is exactly 20 bytes: 10-digit offset, generation, type, SP LF.
		entry := fmt.Sprintf("%010d 00000 n \n", offsets[i])
		if len(entry) != 20 {
			return nil, errFast("xref width")
		}
		buf.WriteString(entry)
	}
	buf.WriteString("trailer\n<</Size ")
	buf.Write(strconv.AppendInt(nil, int64(len(ids)+1), 10))
	buf.WriteString("/Root ")
	buf.Write(strconv.AppendInt(nil, int64(renumber[root]), 10))
	buf.WriteString(" 0 R")
	if info != 0 {
		n, ok := renumber[info]
		if !ok {
			return nil, errFast("info")
		}
		buf.WriteString("/Info ")
		buf.Write(strconv.AppendInt(nil, int64(n), 10))
		buf.WriteString(" 0 R")
	}
	buf.WriteString(">>\nstartxref\n")
	buf.Write(strconv.AppendInt(nil, int64(xrefAt), 10))
	buf.WriteString("\n%%EOF\n")
	return buf.Bytes(), nil
}

func rewriteValue(raw []byte, renumber map[int]int) ([]byte, error) {
	s := pdfScan{b: raw, i: 0}
	st := &scanState{renumber: renumber}
	var w bytes.Buffer
	if err := s.scanValue(&w, st, 0); err != nil {
		return nil, err
	}
	s.skipWS()
	if s.i != len(raw) {
		return nil, errFast("trailing")
	}
	return w.Bytes(), nil
}

type scanState struct {
	refs          *[]int
	renumber      map[int]int
	skipRefs      bool
	captureLength bool
	inLength      bool
	length        int
	lengthSet     bool
	lengthRef     int
	trailer       bool
	root          int
	info          int
	prev          int
	encrypt       bool
	xrefStm       bool
	probe         bool
	sawRef        bool
	sawInt        bool
	probedRef     int
	probedInt     int
}

type pdfScan struct {
	b []byte
	i int
}

func (s *pdfScan) readHeader() (int, int, error) {
	s.skipWS()
	isInt, num, err := s.readNumber()
	if err != nil || !isInt {
		return 0, 0, errFast("object")
	}
	s.skipWS()
	isInt, gen, err := s.readNumber()
	if err != nil || !isInt {
		return 0, 0, errFast("generation")
	}
	s.skipWS()
	if !s.consumeKeyword("obj") {
		return 0, 0, errFast("obj")
	}
	return num, gen, nil
}

func (s *pdfScan) consumeEOL() bool {
	if s.i >= len(s.b) {
		return false
	}
	switch s.b[s.i] {
	case '\r':
		s.i++
		if s.i < len(s.b) && s.b[s.i] == '\n' {
			s.i++
		}
		return true
	case '\n':
		s.i++
		return true
	default:
		return false
	}
}

func (s *pdfScan) skipWS() {
	for s.i < len(s.b) {
		c := s.b[s.i]
		if c == '%' {
			for s.i < len(s.b) && s.b[s.i] != '\n' && s.b[s.i] != '\r' {
				s.i++
			}
			continue
		}
		if isWS(c) {
			s.i++
			continue
		}
		return
	}
}

func (s *pdfScan) consumeKeyword(kw string) bool {
	if s.i+len(kw) > len(s.b) || string(s.b[s.i:s.i+len(kw)]) != kw {
		return false
	}
	end := s.i + len(kw)
	if end < len(s.b) && !isWS(s.b[end]) && !isDelim(s.b[end]) {
		return false
	}
	s.i = end
	return true
}

func (s *pdfScan) readNumber() (bool, int, error) {
	start := s.i
	if s.i >= len(s.b) {
		return false, 0, errNotNumber
	}
	if s.b[s.i] == '+' || s.b[s.i] == '-' {
		s.i++
	}
	digits := false
	isInt := true
	if s.i < len(s.b) && isDigit(s.b[s.i]) {
		digits = true
		for s.i < len(s.b) && isDigit(s.b[s.i]) {
			s.i++
		}
	}
	if s.i < len(s.b) && s.b[s.i] == '.' {
		isInt = false
		s.i++
		if s.i < len(s.b) && isDigit(s.b[s.i]) {
			digits = true
			for s.i < len(s.b) && isDigit(s.b[s.i]) {
				s.i++
			}
		}
	}
	if !digits {
		s.i = start
		return false, 0, errNotNumber
	}
	if !isInt {
		return false, 0, nil
	}
	v, err := strconv.Atoi(string(s.b[start:s.i]))
	if err != nil {
		// Too big to be an object number. Copy it through as an opaque token.
		return false, 0, nil
	}
	return true, v, nil
}

func (s *pdfScan) scanValue(w *bytes.Buffer, st *scanState, depth int) error {
	s.skipWS()
	if s.i >= len(s.b) {
		return io.ErrUnexpectedEOF
	}
	switch s.b[s.i] {
	case '<':
		if s.i+1 < len(s.b) && s.b[s.i+1] == '<' {
			return s.scanDict(w, st, depth)
		}
		return s.copyHex(w)
	case '[':
		return s.scanArray(w, st, depth)
	case '(':
		return s.copyLiteral(w)
	case '/':
		raw, _, err := s.readName()
		if err != nil {
			return err
		}
		if w != nil {
			w.Write(raw)
		}
		return nil
	case 't', 'f', 'n':
		return s.copyKeyword(w)
	default:
		return s.copyNumberOrRef(w, st)
	}
}

func (s *pdfScan) scanDict(w *bytes.Buffer, st *scanState, depth int) error {
	if s.i+1 >= len(s.b) || s.b[s.i] != '<' || s.b[s.i+1] != '<' {
		return errFast("dict")
	}
	s.i += 2
	if w != nil {
		w.WriteString("<<")
	}
	for {
		s.skipWS()
		if s.i+1 < len(s.b) && s.b[s.i] == '>' && s.b[s.i+1] == '>' {
			s.i += 2
			if w != nil {
				w.WriteString("\n>>")
			}
			return nil
		}
		rawName, decoded, err := s.readName()
		if err != nil {
			return err
		}
		if st != nil && !st.skipRefs && structKey(decoded) {
			if err := s.scanValue(nil, &scanState{skipRefs: true}, depth+1); err != nil {
				return err
			}
			continue
		}
		if st != nil && st.trailer && depth == 0 && trailerKey(decoded) {
			if err := s.noteTrailer(st, decoded, depth); err != nil {
				return err
			}
			continue
		}
		if w != nil {
			w.WriteByte('\n')
			w.Write(rawName)
			w.WriteByte(' ')
		}
		if st != nil && depth == 0 && st.captureLength && decoded == "Length" {
			st.inLength = true
			err = s.scanValue(w, st, depth+1)
			st.inLength = false
			if err != nil {
				return err
			}
			continue
		}
		if err := s.scanValue(w, st, depth+1); err != nil {
			return err
		}
	}
}

func (s *pdfScan) noteTrailer(st *scanState, key string, depth int) error {
	probe := &scanState{probe: true}
	if err := s.scanValue(nil, probe, depth+1); err != nil {
		return err
	}
	switch key {
	case "Root":
		if !probe.sawRef || probe.probedRef == 0 {
			return errFast("root")
		}
		st.root = probe.probedRef
	case "Info":
		if probe.sawRef {
			st.info = probe.probedRef
		}
	case "Prev":
		if probe.sawInt {
			st.prev = probe.probedInt
		}
	case "Encrypt":
		st.encrypt = true
	case "XRefStm":
		st.xrefStm = true
	}
	return nil
}

func structKey(name string) bool {
	switch name {
	case "StructTreeRoot", "MarkInfo", "SE":
		return true
	default:
		return false
	}
}

func trailerKey(name string) bool {
	switch name {
	case "Root", "Info", "Prev", "Encrypt", "XRefStm":
		return true
	default:
		return false
	}
}

func (s *pdfScan) scanArray(w *bytes.Buffer, st *scanState, depth int) error {
	s.i++
	if w != nil {
		w.WriteByte('[')
	}
	for {
		s.skipWS()
		if s.i >= len(s.b) {
			return io.ErrUnexpectedEOF
		}
		if s.b[s.i] == ']' {
			s.i++
			if w != nil {
				w.WriteByte(']')
			}
			return nil
		}
		if w != nil {
			w.WriteByte(' ')
		}
		if err := s.scanValue(w, st, depth+1); err != nil {
			return err
		}
	}
}

func (s *pdfScan) copyLiteral(w *bytes.Buffer) error {
	if s.i >= len(s.b) || s.b[s.i] != '(' {
		return errFast("string")
	}
	start := s.i
	s.i++
	depth := 1
	for s.i < len(s.b) && depth > 0 {
		c := s.b[s.i]
		if c == '\\' {
			s.i++
			if s.i >= len(s.b) {
				return io.ErrUnexpectedEOF
			}
			if s.b[s.i] == '\r' {
				s.i++
				if s.i < len(s.b) && s.b[s.i] == '\n' {
					s.i++
				}
				continue
			}
			s.i++
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		}
		s.i++
	}
	if depth != 0 {
		return io.ErrUnexpectedEOF
	}
	if w != nil {
		w.Write(s.b[start:s.i])
	}
	return nil
}

func (s *pdfScan) copyHex(w *bytes.Buffer) error {
	start := s.i
	s.i++
	for s.i < len(s.b) && s.b[s.i] != '>' {
		s.i++
	}
	if s.i >= len(s.b) {
		return io.ErrUnexpectedEOF
	}
	s.i++
	if w != nil {
		w.Write(s.b[start:s.i])
	}
	return nil
}

func (s *pdfScan) readName() ([]byte, string, error) {
	if s.i >= len(s.b) || s.b[s.i] != '/' {
		return nil, "", errFast("name")
	}
	start := s.i
	s.i++
	var dec []byte
	for s.i < len(s.b) {
		c := s.b[s.i]
		if isWS(c) || isDelim(c) {
			break
		}
		if c == '#' {
			if s.i+2 >= len(s.b) {
				return nil, "", errFast("name escape")
			}
			h, err := strconv.ParseUint(string(s.b[s.i+1:s.i+3]), 16, 8)
			if err != nil {
				return nil, "", errFast("name escape")
			}
			dec = append(dec, byte(h))
			s.i += 3
			continue
		}
		dec = append(dec, c)
		s.i++
	}
	return s.b[start:s.i], string(dec), nil
}

func (s *pdfScan) copyKeyword(w *bytes.Buffer) error {
	start := s.i
	for s.i < len(s.b) && !isWS(s.b[s.i]) && !isDelim(s.b[s.i]) {
		s.i++
	}
	word := s.b[start:s.i]
	switch string(word) {
	case "true", "false", "null":
		if w != nil {
			w.Write(word)
		}
		return nil
	default:
		return errFast("keyword")
	}
}

func (s *pdfScan) copyNumberOrRef(w *bytes.Buffer, st *scanState) error {
	start := s.i
	isInt, val, err := s.readNumber()
	if err != nil {
		return errFast("number")
	}
	numEnd := s.i
	if isInt && val >= 0 {
		saved := s.i
		s.skipWS()
		isInt2, gen, err2 := s.readNumber()
		if err2 == nil && isInt2 && gen >= 0 {
			s.skipWS()
			if s.consumeKeyword("R") {
				return s.noteRef(w, st, val, gen)
			}
		}
		s.i = saved
	}
	if st != nil && st.probe && isInt {
		st.probedInt = val
		st.sawInt = true
		return nil
	}
	if st != nil && st.inLength && isInt {
		st.length = val
		st.lengthSet = true
	}
	if w != nil {
		w.Write(s.b[start:numEnd])
	}
	return nil
}

func (s *pdfScan) noteRef(w *bytes.Buffer, st *scanState, val, gen int) error {
	if st != nil && st.skipRefs {
		return nil
	}
	if st != nil && st.probe {
		st.probedRef = val
		st.sawRef = true
		return nil
	}
	if val == 0 {
		return errFast("ref 0")
	}
	if st != nil && st.inLength {
		st.lengthRef = val
	}
	if st != nil && st.refs != nil {
		*st.refs = append(*st.refs, val)
	}
	n, g := val, gen
	if st != nil && st.renumber != nil {
		nn, ok := st.renumber[val]
		if !ok {
			return errFast("dangling ref")
		}
		n, g = nn, 0
	}
	if w != nil {
		w.Write(strconv.AppendInt(nil, int64(n), 10))
		w.WriteString(" ")
		w.Write(strconv.AppendInt(nil, int64(g), 10))
		w.WriteString(" R")
	}
	return nil
}

func isWS(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	default:
		return false
	}
}

var errNotNumber = fmt.Errorf("printkit: strip fast: not a number")
