package main

import (
	"time"
	_ "time/tzdata" // So the check does not depend on the host having zone files.

	"github.com/knadh/listmonk/models"
)

// sendTZKey is the campaign attribs key where the scheduling screen keeps the time zone the
// send time was chosen in (LST-50). send_at stays one moment; the key only decides how the
// screens show it. The frontend (schedule-time.js, TZ_ATTRIB) uses the same name.
const sendTZKey = "axm_send_tz"

// validSendTZ is true when attribs has no send time zone, or it is the name of a time zone
// ("Asia/Seoul"). A non-string, an empty string, a name with spaces or a line break, "Local",
// and an unknown name are not valid.
func validSendTZ(attribs models.JSON) bool {
	v, ok := attribs[sendTZKey]
	if !ok {
		return true
	}

	name, ok := v.(string)
	if !ok || name == "" || len(name) > 64 || name == "Local" {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}

	_, err := time.LoadLocation(name)
	return err == nil
}
