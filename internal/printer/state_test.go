package printer

import "testing"

// Merge складывает дельты в одну картину, незаполненные поля дельты
// прежние значения не затирают (printProgress после второй дельты на месте).
func TestStateMerge(t *testing.T) {
	s := NewState()
	if err := s.Merge([]byte(`{"printProgress":25,"printFileName":"a.gcode"}`)); err != nil {
		t.Fatalf("Merge 1: %v", err)
	}
	if err := s.Merge([]byte(`{"nozzleTemp":"210.43","printLeftTime":7581}`)); err != nil {
		t.Fatalf("Merge 2: %v", err)
	}
	p, _ := s.Int("printProgress")
	fn, _ := s.Str("printFileName")
	nt, _ := s.Str("nozzleTemp")
	if p != 25 || fn != "a.gcode" || nt == "" {
		t.Fatalf("merge дельт не сложился: progress=%d fn=%q nt=%q (dump: %s)", p, fn, nt, s.Dump())
	}
	if err := s.Merge([]byte(`not a json`)); err == nil {
		t.Error("Merge не-JSON: ожидалась ошибка")
	}
}
