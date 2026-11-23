package subimporter

import (
	"strings"
	"testing"
)

// LST-53: what the importer keeps of the rows it refuses.
func TestRejectedRowsBelongToTheirImport(t *testing.T) {
	im := &Importer{status: Status{AxmRejected: map[string]int{}}, importSeq: 7}
	im.reject(7, RejectedRow{Row: 2, Email: "a@gmail.con", Reason: RejectTypo, Suggest: "a@gmail.com"})
	// A session of an import that a newer one replaced writes nothing.
	im.importSeq = 8
	im.owner = 5
	im.status = Status{AxmRejected: map[string]int{}}
	im.reject(7, RejectedRow{Row: 3, Email: "old@gmail.con", Reason: RejectTypo})
	if im.status.AxmRejectedTotal != 0 || len(im.rejected) != 0 {
		t.Fatalf("a replaced session counted a row: %+v %v", im.status, im.rejected)
	}
	im.reject(8, RejectedRow{Row: 3, Email: "new@gmail.con", Reason: RejectTypo})
	if rows, err := im.GetRejected(8, 5, false); err != nil || len(rows) != 1 || rows[0].Email != "new@gmail.con" {
		t.Fatalf("rows %v %v", rows, err)
	}
	if _, err := im.GetRejected(8, 6, false); err != ErrRejectedDenied {
		t.Fatalf("another user: %v", err)
	}
	if _, err := im.GetRejected(8, 6, true); err != nil {
		t.Fatalf("a user who reads all: %v", err)
	}
	if _, err := im.GetRejected(7, 5, false); err != ErrRejectedGone {
		t.Fatalf("an old number: %v", err)
	}
}

func TestLongAddressesAreKeptWhole(t *testing.T) {
	long := strings.Repeat("a", 214) + "@gmail.con"
	im := &Importer{status: Status{AxmRejected: map[string]int{}}, importSeq: 1}
	im.rejectExcluded(1, ExcludedRow{Row: 4, Email: long, Reason: RejectTypo, Suggest: strings.TrimSuffix(long, "n")})
	if got := im.rejected[0]; got.Email != long || got.Suggest != strings.TrimSuffix(long, "n") {
		t.Fatalf("cut: %d %d", len(got.Email), len(got.Suggest))
	}
}

func TestRowNumbersFollowTheChosenFile(t *testing.T) {
	s := &Session{opt: SessionOpt{AxmSourceRows: []int{2, 5, 9}}}
	if s.rowOf(2, 3) != 5 || s.rowOf(3, 4) != 9 {
		t.Fatal("the mapped rows are numbered by their source")
	}
	if s.rowOf(4, 12) != 12 || (&Session{}).rowOf(1, 6) != 6 {
		t.Fatal("without a source the line of the uploaded file is used")
	}
}
