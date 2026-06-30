package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
}

type SendMessageRequest struct {
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

type deleteMessageRequest struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int64 `json:"message_id"`
}

type apiError struct {
	Method      string
	StatusCode  int
	Description string
}

func (e *apiError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("telegram %s failed with status %d: %s", e.Method, e.StatusCode, e.Description)
	}
	return fmt.Sprintf("telegram %s failed: %s", e.Method, e.Description)
}

func NewClient(token string, logger *slog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(defaultBaseURL, "/") + "/bot" + token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var result User
	if err := c.postJSON(ctx, "getMe", nil, &result); err != nil {
		return User{}, err
	}
	return result, nil
}

func (c *Client) SetWebhook(ctx context.Context, request SetWebhookRequest) error {
	var result bool
	return c.postJSON(ctx, "setWebhook", request, &result)
}

func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var result WebhookInfo
	if err := c.postJSON(ctx, "getWebhookInfo", nil, &result); err != nil {
		return WebhookInfo{}, err
	}
	return result, nil
}

func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	var result bool
	return c.postJSON(ctx, "deleteMessage", deleteMessageRequest{
		ChatID:    chatID,
		MessageID: messageID,
	}, &result)
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) (Message, error) {
	var result Message
	if err := c.postJSON(ctx, "sendMessage", SendMessageRequest{
		ChatID: chatID,
		Text:   text,
	}, &result); err != nil {
		return Message{}, err
	}
	return result, nil
}

func (c *Client) postJSON(ctx context.Context, method string, payload any, dst any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal %s request: %w", method, err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method, body)
	if err != nil {
		return fmt.Errorf("create %s request: %w", method, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute %s request: %w", method, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read %s response: %w", method, err)
	}

	if resp.StatusCode != http.StatusOK {
		return &apiError{
			Method:      method,
			StatusCode:  resp.StatusCode,
			Description: strings.TrimSpace(string(bodyBytes)),
		}
	}

	var envelope apiResponse[json.RawMessage]
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if !envelope.OK {
		return &apiError{
			Method:      method,
			Description: envelope.Description,
		}
	}

	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, dst); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}

	return nil
}
