package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/alerts"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/notify"
)

// notificationRoutes are admin only. Changing a channel is browser-only, like
// the S3 destination: a channel decides where event details are sent, which
// is not something a leaked token should be able to redirect.
func (a *API) notificationRoutes(r chi.Router) {
	r.Use(auth.RequireRole("admin"))
	r.Get("/kinds", handleNotificationKinds)
	r.Get("/channels", a.handleListChannels)
	r.Get("/deliveries", a.handleListDeliveries)
	r.Get("/settings", a.handleGetAlertSettings)
	r.Group(func(r chi.Router) {
		r.Use(auth.RejectToken)
		r.Post("/channels", a.handleCreateChannel)
		r.Put("/channels/{id}", a.handleUpdateChannel)
		r.Delete("/channels/{id}", a.handleDeleteChannel)
		r.Post("/channels/{id}/test", a.handleTestChannel)
		r.Put("/settings", a.handlePutAlertSettings)
	})
}

func handleNotificationKinds(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"events":   notify.Kinds,
		"channels": []string{notify.ChannelWebhook, notify.ChannelNtfy, notify.ChannelDiscord, notify.ChannelSlack, notify.ChannelEmail},
	})
}

func (a *API) handleListChannels(w http.ResponseWriter, r *http.Request) {
	chs, err := a.Notify.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chs)
}

func (a *API) handleListDeliveries(w http.ResponseWriter, r *http.Request) {
	limit := int64(50)
	if v, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	ds, err := a.Notify.Recent(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func channelID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return 0, false
	}
	return id, true
}

func (a *API) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var in notify.ChannelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	ch, err := a.Notify.Create(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "notification.channel_create", "notification_channel", strconv.FormatInt(ch.ID, 10), remoteIP(r),
		map[string]any{"name": ch.Name, "kind": ch.Kind})
	writeJSON(w, http.StatusCreated, ch)
}

func (a *API) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := channelID(w, r)
	if !ok {
		return
	}
	var in notify.ChannelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	ch, err := a.Notify.Update(r.Context(), id, in)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "channel not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "notification.channel_update", "notification_channel", strconv.FormatInt(id, 10), remoteIP(r),
		map[string]any{"name": ch.Name, "enabled": ch.Enabled})
	writeJSON(w, http.StatusOK, ch)
}

func (a *API) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := channelID(w, r)
	if !ok {
		return
	}
	if err := a.Notify.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "notification.channel_delete", "notification_channel", strconv.FormatInt(id, 10), remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleTestChannel sends straight away and answers with the real error, so
// a wrong URL or password shows on the screen rather than in the outbox.
func (a *API) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := channelID(w, r)
	if !ok {
		return
	}
	err := a.Notify.Test(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "channel not found")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	detail := map[string]any{"ok": err == nil}
	if err != nil {
		detail["error"] = err.Error()
	}
	a.Audit.Write(r.Context(), user.ID, "notification.channel_test", "notification_channel", strconv.FormatInt(id, 10), remoteIP(r), detail)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleGetAlertSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, alerts.LoadSettingsJSON(r.Context(), a.Queries))
}

func (a *API) handlePutAlertSettings(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	var in alerts.SettingsJSON
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := alerts.SaveSettings(r.Context(), a.Queries, in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	a.Attention.Invalidate()
	a.Audit.Write(r.Context(), user.ID, "notification.settings_update", "settings", "alerts", remoteIP(r), in)
	writeJSON(w, http.StatusOK, alerts.LoadSettingsJSON(r.Context(), a.Queries))
}
