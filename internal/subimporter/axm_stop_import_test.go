package subimporter

import "testing"

// StopImport clears a finished import only when the number is that of the import held (LST-63).
func TestStopImportOnlyThatImport(t *testing.T) {
	im := &Importer{status: Status{Status: StatusFinished, AxmImportID: 7, Imported: 3}}
	if st, done := im.StopImport(6); done || st.Status != StatusFinished || st.Imported != 3 {
		t.Fatalf("%v %+v", done, st)
	}
	if st, done := im.StopImport(7); !done || st.Status != StatusNone {
		t.Fatalf("%v %+v", done, st)
	}
}
