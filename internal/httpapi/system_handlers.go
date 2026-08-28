package httpapi

import "net/http"

// SystemStatus supplies the current process lifecycle to health handlers.
type SystemStatus interface {
	Live() bool
	Ready() bool
}

type systemHandlers struct {
	status SystemStatus
}

func (h *systemHandlers) live(w http.ResponseWriter, _ *http.Request) {
	h.writeHealth(w, h.status == nil || h.status.Live())
}

func (h *systemHandlers) ready(w http.ResponseWriter, _ *http.Request) {
	h.writeHealth(w, h.status == nil || h.status.Ready())
}

func (*systemHandlers) writeHealth(w http.ResponseWriter, healthy bool) {
	if !healthy {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
