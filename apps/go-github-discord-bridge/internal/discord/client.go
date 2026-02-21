package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	DiscordAPIBase = "https://discord.com/api/v10"
)

type Client struct {
	token          string
	forumChannelID string
	httpClient     *http.Client
}

// NewClient 建立 Discord API client
func NewClient(token, forumChannelID string) *Client {
	return &Client{
		token:          token,
		forumChannelID: forumChannelID,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CreateThreadRequest 建立 thread 的請求結構
type CreateThreadRequest struct {
	Name        string        `json:"name"`                    // Thread 標題
	Message     ThreadMessage `json:"message"`                 // 第一則訊息
	AppliedTags []string      `json:"applied_tags,omitempty"`  // Forum tags (可選)
}

type ThreadMessage struct {
	Content string  `json:"content,omitempty"` // 純文字內容
	Embeds  []Embed `json:"embeds,omitempty"`  // Rich embed
}

// Embed Discord 的 rich embed 結構
type Embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	URL         string       `json:"url,omitempty"`
	Color       int          `json:"color,omitempty"` // 顏色（整數）
	Fields      []EmbedField `json:"fields,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"` // ISO 8601 format
	Footer      *EmbedFooter `json:"footer,omitempty"`
}

type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type EmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

// CreateThreadResponse Discord API 的回應
type CreateThreadResponse struct {
	ID   string `json:"id"`   // Thread ID
	Name string `json:"name"` // Thread 名稱
}

// ForumTag Discord forum channel 的 tag 結構
type ForumTag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ForumChannelResponse Discord channel 資訊（用於取得 available_tags）
type ForumChannelResponse struct {
	AvailableTags []ForumTag `json:"available_tags"`
}

// GetOrCreateRepoTag 取得或建立 repo 對應的 forum tag，回傳 tag ID
// 如果 forum 已有同名 tag 就直接用，沒有就建立新的
func (c *Client) GetOrCreateRepoTag(repoName string) (string, error) {
	// 取得 forum channel 資訊
	url := fmt.Sprintf("%s/channels/%s", DiscordAPIBase, c.forumChannelID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get channel: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord API error (status %d): %s", resp.StatusCode, string(body))
	}

	var channel ForumChannelResponse
	if err := json.Unmarshal(body, &channel); err != nil {
		return "", fmt.Errorf("failed to parse channel: %w", err)
	}

	// 找已存在的 tag
	for _, tag := range channel.AvailableTags {
		if tag.Name == repoName {
			return tag.ID, nil
		}
	}

	// 建立新 tag（透過 PATCH channel，加入新的 available_tags）
	newTags := append(channel.AvailableTags, ForumTag{Name: repoName})

	type PatchBody struct {
		AvailableTags []ForumTag `json:"available_tags"`
	}
	patchData, err := json.Marshal(PatchBody{AvailableTags: newTags})
	if err != nil {
		return "", fmt.Errorf("failed to marshal patch: %w", err)
	}

	patchReq, err := http.NewRequest("PATCH", url, bytes.NewBuffer(patchData))
	if err != nil {
		return "", fmt.Errorf("failed to create patch request: %w", err)
	}
	patchReq.Header.Set("Authorization", "Bot "+c.token)
	patchReq.Header.Set("Content-Type", "application/json")

	patchResp, err := c.httpClient.Do(patchReq)
	if err != nil {
		return "", fmt.Errorf("failed to patch channel: %w", err)
	}
	defer patchResp.Body.Close()

	patchBody, _ := io.ReadAll(patchResp.Body)
	if patchResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord API error on patch (status %d): %s", patchResp.StatusCode, string(patchBody))
	}

	// 重新解析拿到新 tag 的 ID
	var updated ForumChannelResponse
	if err := json.Unmarshal(patchBody, &updated); err != nil {
		return "", fmt.Errorf("failed to parse updated channel: %w", err)
	}

	for _, tag := range updated.AvailableTags {
		if tag.Name == repoName {
			return tag.ID, nil
		}
	}

	return "", fmt.Errorf("tag created but not found in response")
}

// CreateThread 在 forum channel 建立新的 thread
func (c *Client) CreateThread(title string, message ThreadMessage, tagIDs ...string) (string, error) {
	url := fmt.Sprintf("%s/channels/%s/threads", DiscordAPIBase, c.forumChannelID)

	reqBody := CreateThreadRequest{
		Name:        title,
		Message:     message,
		AppliedTags: tagIDs,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("discord API error (status %d): %s", resp.StatusCode, string(body))
	}

	var result CreateThreadResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return result.ID, nil
}

// PostMessage 在已存在的 thread 中發送訊息
func (c *Client) PostMessage(threadID string, message ThreadMessage) error {
	url := fmt.Sprintf("%s/channels/%s/messages", DiscordAPIBase, threadID)

	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord API error (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// ArchiveThreadRequest archive thread 的請求
type ArchiveThreadRequest struct {
	Archived bool `json:"archived"`
}

// ArchiveThread 關閉並 archive 一個 thread
func (c *Client) ArchiveThread(threadID string) error {
	url := fmt.Sprintf("%s/channels/%s", DiscordAPIBase, threadID)

	reqBody := ArchiveThreadRequest{
		Archived: true,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord API error (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
