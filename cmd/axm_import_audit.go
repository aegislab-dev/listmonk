package main

import (
	"encoding/json"
	"strings"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/labstack/echo/v4"
)

// importActor is who started the import that runs now. Only one import runs at a time, so
// the end of it (the importer's PostCB) is written as an action of that user.
type importActor struct {
	user auth.User
	ok   bool
}

// importStarted writes the import.start row and remembers who started the import.
// The file name is not stored; the list names and the options are.
func (a *App) importStarted(c echo.Context, opt subimporter.SessionOpt) {
	u := auth.GetUser(c)
	a.importMu.Lock()
	a.importBy = importActor{user: u, ok: true}
	a.importLists = nil
	if opt.Mode == subimporter.ModeSubscribe {
		a.importLists = append([]int{}, opt.ListIDs...)
	}
	a.importMu.Unlock()

	a.audit(c, auditImportStart, auditTarget{Type: auditTargetImport}, auditNoCount, map[string]any{
		"mode": opt.Mode, "subscription_status": opt.SubStatus,
		"lists":                         a.listRefs(opt.ListIDs),
		"overwrite_userinfo":            opt.Overwrite || opt.OverwriteUserInfo,
		"overwrite_subscription_status": opt.Overwrite || opt.OverwriteSubStatus,
	})
}

// auditImportFinish is called when the importer reports the end of an import (its PostCB).
// data is the importer's status template: Name, Status, Imported and Total.
func (a *App) auditImportFinish(data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	var st struct {
		Status   string
		Imported int
		Total    int
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return
	}
	a.importMu.Lock()
	by := a.importBy
	a.importMu.Unlock()

	var u *auth.User
	if by.ok {
		u = &by.user
	}
	a.auditAs(u, auditImportFinish, auditTarget{Type: auditTargetImport}, st.Imported, map[string]any{
		"status": strings.ToLower(st.Status), "total": st.Total,
	})
}
