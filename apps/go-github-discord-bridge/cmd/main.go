package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"dizzycode1112/github-discord-bridge/internal/config"
	"dizzycode1112/github-discord-bridge/internal/discord"
	"dizzycode1112/github-discord-bridge/internal/github"
	"dizzycode1112/github-discord-bridge/internal/storage"
	"dizzycode1112/github-discord-bridge/pkg/applogger"

	"github.com/gin-gonic/gin"
)

type App struct {
	store         storage.Store
	discordClient *discord.Client
	githubSecret  string
}

func main() {
	config.Load()
	cfg := config.AppConfig

	// 初始化 logger
	applogger.Init(cfg.Env)
	log := applogger.Log
	defer log.Flush()

	// 初始化 storage
	store, err := storage.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Error("Failed to connect to Redis", "error", err)
		panic(err)
	}
	defer store.Close()

	// 初始化 Discord client
	discordClient := discord.NewClient(cfg.DiscordBotToken, cfg.DiscordForumChID)

	app := &App{
		store:         store,
		discordClient: discordClient,
		githubSecret:  cfg.GitHubWebhookSecret,
	}

	// 設定 Gin router
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	r.POST("/webhook/github", app.handleGitHubWebhook)

	log.Info("Server starting", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Error("Failed to start server", "error", err)
		panic(err)
	}
}

func (app *App) handleGitHubWebhook(c *gin.Context) {
	log := applogger.Log

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": "failed to read body"})
		return
	}

	// 驗證 webhook signature
	if app.githubSecret != "" {
		signature := c.GetHeader("X-Hub-Signature-256")
		if signature == "" {
			c.JSON(401, gin.H{"error": "missing signature"})
			return
		}

		if !verifySignature(body, signature, app.githubSecret) {
			c.JSON(401, gin.H{"error": "invalid signature"})
			return
		}
	}

	// 處理 ping event（GitHub 建立 webhook 時發送）
	ghEvent := c.GetHeader("X-GitHub-Event")
	if ghEvent == "ping" {
		log.Info("Received GitHub ping")
		c.JSON(200, gin.H{"status": "pong"})
		return
	}

	// 解析 webhook payload（body 已被 ReadAll 消耗，用 json.Unmarshal）
	var payload github.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Error("Failed to parse webhook payload", "error", err)
		c.JSON(400, gin.H{"error": "invalid payload"})
		return
	}

	log.Info("Received GitHub event", "ghEvent", ghEvent, "action", payload.Action)

	// check_suite 獨立處理（payload 不一定有 pull_request，不走 handleEvent）
	// handleCheckSuiteCompleted 內部對個別 PR 的失敗用 continue 跳過，
	// 這裡的 err 只處理整體性錯誤（例如 check_suite 欄位缺失），回 500 讓 GitHub retry。
	if ghEvent == "workflow_run" {
		if payload.Action == "completed" {
			if err := app.handleWorkflowRunCompleted(&payload); err != nil {
				log.Error("Failed to handle workflow_run", "error", err)
				c.JSON(500, gin.H{"error": "failed to process event"})
				return
			}
		}
		c.JSON(200, gin.H{"status": "processed"})
		return
	}

	if err := app.handleEvent(ghEvent, &payload); err != nil {
		log.Error("Failed to handle event", "ghEvent", ghEvent, "action", payload.Action, "error", err)
		c.JSON(500, gin.H{"error": "failed to process event"})
		return
	}

	c.JSON(200, gin.H{"status": "processed"})
}

func (app *App) handleEvent(ghEvent string, payload *github.WebhookPayload) error {
	log := applogger.Log

	pr := payload.PullRequest
	if pr == nil {
		log.Warn("No pull request in payload, ignoring", "ghEvent", ghEvent, "action", payload.Action)
		return nil
	}

	prID := payload.GetPRIdentifier()
	if prID == "" {
		return fmt.Errorf("failed to get PR identifier")
	}

	switch ghEvent {
	case "pull_request":
		switch payload.Action {
		case "opened":
			return app.handlePROpened(prID, pr)
		case "synchronize":
			return app.handlePRUpdated(prID, pr)
		case "closed":
			if pr.Merged {
				return app.handlePRMerged(prID, pr, payload.Sender.Login)
			}
			return app.handlePRClosed(prID, pr, payload.Sender.Login)
		case "reopened":
			return app.handlePRReopened(prID, pr)
		case "review_requested":
			return app.handleReviewRequested(prID, pr, payload.RequestedReviewer, payload.Sender.Login)
		case "review_request_removed", "edited", "labeled", "unlabeled", "assigned", "unassigned":
			return nil
		default:
			log.Warn("Unhandled pull_request action", "action", payload.Action)
			return nil
		}
	case "pull_request_review":
		if payload.Action != "submitted" {
			log.Info("Ignoring pull_request_review action", "action", payload.Action)
			return nil
		}
		return app.handlePRReviewed(prID, pr, payload.Review)
	case "issue_comment", "pull_request_review_comment":
		log.Info("Ignoring comment event", "ghEvent", ghEvent)
		return nil
	default:
		log.Warn("Unhandled GitHub event", "ghEvent", ghEvent)
		return nil
	}
}

func (app *App) handlePROpened(prID string, pr *github.PullRequest) error {
	log := applogger.Log

	if existingThreadID, exists, _ := app.store.Get(prID); exists {
		log.Info("Thread already exists", "prID", prID, "threadID", existingThreadID)
		return nil
	}

	title := discord.FormatThreadTitle(pr.Number, pr.Title)
	message := discord.FormatPROpened(pr)

	threadID, err := app.discordClient.CreateThread(title, message)
	if err != nil {
		return fmt.Errorf("failed to create thread: %w", err)
	}

	if err := app.store.Set(prID, threadID); err != nil {
		return fmt.Errorf("failed to save mapping: %w", err)
	}

	log.Info("Created thread", "prID", prID, "threadID", threadID)
	return nil
}

func (app *App) handlePRUpdated(prID string, pr *github.PullRequest) error {
	log := applogger.Log

	threadID, exists, err := app.store.Get(prID)
	if err != nil {
		return err
	}

	if !exists {
		log.Info("Thread not found, auto-creating", "prID", prID)
		if err := app.handlePROpened(prID, pr); err != nil {
			return fmt.Errorf("failed to auto-create thread: %w", err)
		}
		threadID, exists, err = app.store.Get(prID)
		if err != nil || !exists {
			return fmt.Errorf("failed to get thread after creation")
		}
	}

	message := discord.FormatPRUpdated(pr)
	return app.discordClient.PostMessage(threadID, message)
}

func (app *App) handleReviewRequested(prID string, pr *github.PullRequest, reviewer *github.User, requestedBy string) error {
	log := applogger.Log

	if reviewer == nil {
		log.Warn("No requested_reviewer in payload", "prID", prID)
		return nil
	}

	threadID, exists, err := app.store.Get(prID)
	if err != nil {
		return err
	}

	if !exists {
		log.Info("Thread not found, auto-creating", "prID", prID)
		if err := app.handlePROpened(prID, pr); err != nil {
			return fmt.Errorf("failed to auto-create thread: %w", err)
		}
		threadID, exists, err = app.store.Get(prID)
		if err != nil || !exists {
			return fmt.Errorf("failed to get thread after creation")
		}
	}

	message := discord.FormatReviewRequested(reviewer, requestedBy, pr.Number, pr.HTMLURL, config.AppConfig.GitHubDiscordUserMap)
	return app.discordClient.PostMessage(threadID, message)
}

func (app *App) handlePRReviewed(prID string, pr *github.PullRequest, review *github.Review) error {
	log := applogger.Log

	threadID, exists, err := app.store.Get(prID)
	if err != nil {
		return err
	}

	if !exists {
		log.Info("Thread not found, auto-creating", "prID", prID)
		if err := app.handlePROpened(prID, pr); err != nil {
			return fmt.Errorf("failed to auto-create thread: %w", err)
		}
		threadID, exists, err = app.store.Get(prID)
		if err != nil || !exists {
			return fmt.Errorf("failed to get thread after creation")
		}
	}

	message := discord.FormatPRReview(review, pr.Number, pr.HTMLURL, pr.User.Login, config.AppConfig.GitHubDiscordUserMap)
	return app.discordClient.PostMessage(threadID, message)
}

func (app *App) handlePRMerged(prID string, pr *github.PullRequest, mergedBy string) error {
	log := applogger.Log

	threadID, exists, err := app.store.Get(prID)
	if err != nil {
		return err
	}

	if !exists {
		log.Info("Thread not found, auto-creating before merge notification", "prID", prID)
		if err := app.handlePROpened(prID, pr); err != nil {
			return fmt.Errorf("failed to auto-create thread: %w", err)
		}
		threadID, exists, err = app.store.Get(prID)
		if err != nil || !exists {
			return fmt.Errorf("failed to get thread after creation")
		}
	}

	message := discord.FormatPRMerged(pr, mergedBy)
	if err := app.discordClient.PostMessage(threadID, message); err != nil {
		return err
	}

	if err := app.discordClient.ArchiveThread(threadID); err != nil {
		log.Error("Failed to archive thread", "prID", prID, "threadID", threadID, "error", err)
	}

	if err := app.store.MarkAsClosed(prID); err != nil {
		log.Error("Failed to mark as closed", "prID", prID, "error", err)
	}

	log.Info("PR merged and thread archived", "prID", prID)
	return nil
}

func (app *App) handlePRClosed(prID string, pr *github.PullRequest, closedBy string) error {
	log := applogger.Log

	threadID, exists, err := app.store.Get(prID)
	if err != nil {
		return err
	}

	if !exists {
		log.Info("Thread not found, auto-creating before close notification", "prID", prID)
		if err := app.handlePROpened(prID, pr); err != nil {
			return fmt.Errorf("failed to auto-create thread: %w", err)
		}
		threadID, exists, err = app.store.Get(prID)
		if err != nil || !exists {
			return fmt.Errorf("failed to get thread after creation")
		}
	}

	message := discord.FormatPRClosed(pr, closedBy)
	if err := app.discordClient.PostMessage(threadID, message); err != nil {
		return err
	}

	if err := app.discordClient.ArchiveThread(threadID); err != nil {
		log.Error("Failed to archive thread", "prID", prID, "threadID", threadID, "error", err)
	}

	if err := app.store.MarkAsClosed(prID); err != nil {
		log.Error("Failed to mark as closed", "prID", prID, "error", err)
	}

	log.Info("PR closed and thread archived", "prID", prID)
	return nil
}

func (app *App) handlePRReopened(prID string, pr *github.PullRequest) error {
	threadID, exists, err := app.store.Get(prID)
	if err != nil {
		return err
	}

	if !exists {
		return app.handlePROpened(prID, pr)
	}

	message := discord.ThreadMessage{
		Embeds: []discord.Embed{
			{
				Title:       "🔄 PR Reopened",
				Description: fmt.Sprintf("**%s** has been reopened", pr.Title),
				URL:         pr.HTMLURL,
				Color:       discord.ColorYellow,
			},
		},
	}

	return app.discordClient.PostMessage(threadID, message)
}

func (app *App) handleWorkflowRunCompleted(payload *github.WebhookPayload) error {
	log := applogger.Log

	wr := payload.WorkflowRun
	if wr == nil {
		log.Warn("No workflow_run in payload")
		return nil
	}

	// 只通知 success 和 failure，其他（cancelled、timed_out 等）不發送
	if wr.Conclusion != "success" && wr.Conclusion != "failure" {
		log.Info("Skipping CI notification", "conclusion", wr.Conclusion, "workflow", wr.Name)
		return nil
	}

	// 只通知有關聯 PR 的 workflow run
	for _, wrPR := range wr.PullRequests {
		prID := fmt.Sprintf("%s#%d", payload.Repository.FullName, wrPR.Number)

		threadID, exists, err := app.store.Get(prID)
		if err != nil {
			log.Error("Failed to get thread", "prID", prID, "error", err)
			continue
		}
		if !exists {
			log.Info("No thread for PR, skipping CI notification", "prID", prID)
			continue
		}

		message := discord.FormatWorkflowRunResult(wr)
		if err := app.discordClient.PostMessage(threadID, message); err != nil {
			log.Error("Failed to post CI notification", "prID", prID, "error", err)
		}
	}

	return nil
}

func verifySignature(payload []byte, signature, secret string) bool {
	if secret == "" {
		return true
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))
	expectedSignature := "sha256=" + expectedMAC

	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}
