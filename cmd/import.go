package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo/v4"
)

// ImportSubscribers handles the uploading and bulk importing of
// a ZIP file of one or more CSV files.
func (a *App) ImportSubscribers(c echo.Context) error {
	// Is an import already running?
	if a.importer.GetStats().Status == subimporter.StatusImporting {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("import.alreadyRunning"))
	}

	// Unmarshal the JSON params.
	var opt subimporter.SessionOpt
	if err := json.Unmarshal([]byte(c.FormValue("params")), &opt); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("import.invalidParams", "error", err.Error()))
	}

	// Filter list IDs against the current user's permitted lists.
	// Blocklist mode doesn't require list subscriptions.
	user := auth.GetUser(c)
	opt.ListIDs = user.FilterListsByPerm(auth.PermTypeManage, opt.ListIDs)
	if len(opt.ListIDs) == 0 && opt.Mode != subimporter.ModeBlocklist {
		return echo.NewHTTPError(http.StatusForbidden,
			a.i18n.Ts("globals.messages.permissionDenied", "name", "lists"))
	}

	// Only a user who may manage all lists overwrites the names and attributes of people who
	// already exist: they may be on other teams' lists too.
	restrictImportOverwrite(user, &opt)

	if err := a.rejectDynamicTargets(opt.ListIDs); err != nil {
		return err
	}

	// Validate mode.
	if opt.Mode != subimporter.ModeSubscribe && opt.Mode != subimporter.ModeBlocklist {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("import.invalidMode"))
	}

	// If no status is specified, pick a default one.
	if opt.SubStatus == "" {
		switch opt.Mode {
		case subimporter.ModeSubscribe:
			opt.SubStatus = models.SubscriptionStatusUnconfirmed
		case subimporter.ModeBlocklist:
			opt.SubStatus = models.SubscriptionStatusUnsubscribed
		}
	}

	if opt.SubStatus != models.SubscriptionStatusUnconfirmed &&
		opt.SubStatus != models.SubscriptionStatusConfirmed &&
		opt.SubStatus != models.SubscriptionStatusUnsubscribed {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("import.invalidSubStatus"))
	}

	if len(opt.Delim) != 1 {
		return echo.NewHTTPError(http.StatusBadRequest, a.i18n.T("import.invalidDelim"))
	}

	// Open the HTTP file.
	file, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest,
			a.i18n.Ts("import.invalidFile", "error", err.Error()))
	}

	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	// Copy it to a temp location.
	out, err := os.CreateTemp("", "listmonk")
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			a.i18n.Ts("import.errorCopyingFile", "error", err.Error()))
	}
	defer out.Close()

	if _, err = io.Copy(out, src); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			a.i18n.Ts("import.errorCopyingFile", "error", err.Error()))
	}

	// Start the importer session.
	opt.Filename = file.Filename
	sess, err := a.newImportSession(opt)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			a.i18n.Ts("import.errorStarting", "error", err.Error()))
	}
	a.importStarted(c, opt)
	go sess.Start()

	if strings.HasSuffix(strings.ToLower(file.Filename), ".csv") {
		go sess.LoadCSV(out.Name(), rune(opt.Delim[0]))
	} else {
		// Only 1 CSV from the ZIP is considered. If multiple files have
		// to be processed, counting the net number of lines (to track progress),
		// keeping the global import state (failed / successful) etc. across
		// multiple files becomes complex. Instead, it's just easier for the
		// end user to concat multiple CSVs (if there are multiple in the first)
		// place and upload as one in the first place.
		dir, files, err := sess.ExtractZIP(out.Name(), 1)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError,
				a.i18n.Ts("import.errorProcessingZIP", "error", err.Error()))
		}

		go sess.LoadCSV(dir+"/"+files[0], rune(opt.Delim[0]))
	}

	return c.JSON(http.StatusOK, okResp{a.importer.GetStats()})
}

// GetImportSubscribers returns import statistics.
func (a *App) GetImportSubscribers(c echo.Context) error {
	s, all := a.importStatus()
	// Only the lists this user may see: the screen checks their addresses. The others are
	// counted so that the screen can say that they were not checked.
	user := auth.GetUser(c)
	lists := append([]int{}, user.FilterListsByPerm(auth.PermTypeGet|auth.PermTypeManage, all)...)
	return c.JSON(http.StatusOK, okResp{struct {
		subimporter.Status
		AxmLists       []int `json:"axm_lists"`
		AxmListsHidden int   `json:"axm_lists_hidden"`
	}{s, lists, len(all) - len(lists)}})
}

// newImportSession creates the import session and records the lists it subscribes people
// to as one step with respect to importStatus.
func (a *App) newImportSession(opt subimporter.SessionOpt) (*subimporter.Session, error) {
	a.importListsMu.Lock()
	defer a.importListsMu.Unlock()
	sess, err := a.importer.NewSession(opt)
	if err != nil {
		return nil, err
	}
	a.importLists = nil
	if opt.Mode == subimporter.ModeSubscribe {
		a.importLists = append([]int{}, opt.ListIDs...)
	}
	return sess, nil
}

// importStatus returns the importer's status together with the lists of that same import
// (none when there is no import or it is a blocklist import). A page never gets the status
// of one import with the lists of another.
func (a *App) importStatus() (subimporter.Status, []int) {
	a.importListsMu.Lock()
	defer a.importListsMu.Unlock()
	s := a.importer.GetStats()
	if s.Status == subimporter.StatusNone {
		return s, nil
	}
	return s, a.importLists
}

// GetImportSubscriberStats returns import statistics.
func (a *App) GetImportSubscriberStats(c echo.Context) error {
	return c.JSON(http.StatusOK, okResp{string(a.importer.GetLogs())})
}

// StopImportSubscribers sends a stop signal to the importer.
// If there's an ongoing import, it'll be stopped, and if an import
// is finished, it's state is cleared.
func (a *App) StopImportSubscribers(c echo.Context) error {
	st := a.importer.GetStats()
	a.importer.Stop()
	if st.Status == subimporter.StatusImporting || st.Status == subimporter.StatusStopping {
		a.audit(c, auditImportStop, auditTarget{Type: auditTargetImport}, auditNoCount, map[string]any{
			"imported": st.Imported, "total": st.Total,
		})
	}
	return c.JSON(http.StatusOK, okResp{a.importer.GetStats()})
}
