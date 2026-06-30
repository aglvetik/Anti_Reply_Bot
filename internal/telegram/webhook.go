package telegram

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

type WebhookHandler struct {
	secret  string
	logger  *slog.Logger
	process func(context.Context, Update)
}

func NewWebhookHandler(secret string, logger *slog.Logger, process func(context.Context, Update)) http.Handler {
	return &WebhookHandler{
		secret:  secret,
		logger:  logger,
		process: process,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	if r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != h.secret {
		h.logger.Warn("reject webhook request with invalid secret")
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	defer r.Body.Close()

	var update Update
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&update); err != nil {
		h.logger.Warn("decode webhook request failed", "error", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	go h.process(context.Background(), update)

	w.WriteHeader(http.StatusOK)
}
