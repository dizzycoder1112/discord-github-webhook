package discord

import (
	"dizzycode1112/github-discord-bridge/internal/github"
	"fmt"
	"time"
)

// Discord 顏色常數（整數格式）
const (
	ColorGreen  = 0x57F287 // PR opened, approved
	ColorYellow = 0xFEE75C // PR review requested
	ColorRed    = 0xED4245 // PR closed without merge
	ColorPurple = 0x5865F2 // PR merged
	ColorGray   = 0x99AAB5 // General info
)

// FormatPROpened 格式化「PR 開啟」的訊息
func FormatPROpened(pr *github.PullRequest) ThreadMessage {
	description := pr.Body
	if len(description) > 500 {
		description = description[:497] + "..."
	}
	if description == "" {
		description = "*No description provided*"
	}

	embed := Embed{
		Title:       fmt.Sprintf("Pull Request #%d Opened", pr.Number),
		Description: description,
		URL:         pr.HTMLURL,
		Color:       ColorGreen,
		Fields: []EmbedField{
			{
				Name:   "Author",
				Value:  fmt.Sprintf("[@%s](%s)", pr.User.Login, pr.User.HTMLURL),
				Inline: true,
			},
			{
				Name:   "Branch",
				Value:  fmt.Sprintf("`%s` → `%s`", pr.Head.Ref, pr.Base.Ref),
				Inline: true,
			},
			{
				Name:   "Changes",
				Value:  fmt.Sprintf("+%d −%d", pr.Additions, pr.Deletions),
				Inline: true,
			},
		},
		Timestamp: pr.CreatedAt.Format(time.RFC3339),
		Footer: &EmbedFooter{
			Text:    "GitHub",
			IconURL: "https://github.githubassets.com/images/modules/logos_page/GitHub-Mark.png",
		},
	}

	return ThreadMessage{
		Embeds: []Embed{embed},
	}
}

// FormatPRReview 格式化「PR Review」的訊息
// prAuthorLogin: PR 作者的 GitHub 帳號，用來查 userMap 取得 Discord ID 做 mention
func FormatPRReview(review *github.Review, prNumber int, prURL string, prAuthorLogin string, userMap map[string]string) ThreadMessage {
	var emoji string
	var color int

	switch review.State {
	case "approved":
		emoji = "✅"
		color = ColorGreen
	case "changes_requested":
		emoji = "🔴"
		color = ColorRed
	case "commented":
		emoji = "💬"
		color = ColorGray
	default:
		emoji = "📝"
		color = ColorGray
	}

	title := fmt.Sprintf("%s Review by @%s", emoji, review.User.Login)

	description := "**" + formatReviewState(review.State) + "**"
	if review.Body != "" {
		body := review.Body
		if len(body) > 800 {
			body = body[:797] + "..."
		}
		description += "\n\n" + body
	}

	embed := Embed{
		Title:       title,
		Description: description,
		URL:         review.HTMLURL,
		Color:       color,
		Timestamp:   review.SubmittedAt.Format(time.RFC3339),
	}

	// 只有 approved / changes_requested 才 mention PR 作者（commented 不打擾）
	var content string
	if review.State == "approved" || review.State == "changes_requested" {
		if discordID, ok := userMap[prAuthorLogin]; ok {
			content = fmt.Sprintf("<@%s>", discordID)
		} else {
			content = fmt.Sprintf("@%s", prAuthorLogin)
		}
	}

	return ThreadMessage{
		Content: content,
		Embeds:  []Embed{embed},
	}
}

// FormatReviewRequested 格式化「Review Requested」的訊息
func FormatReviewRequested(reviewer *github.User, requestedBy string, prNumber int, prURL string, userMap map[string]string) ThreadMessage {
	// Discord mention 只在 content 才有效，embed title/description 不支援
	var content string
	if discordID, ok := userMap[reviewer.Login]; ok {
		content = fmt.Sprintf("<@%s>", discordID)
	}

	embed := Embed{
		Title:       fmt.Sprintf("🔔 Review requested from @%s", reviewer.Login),
		Description: fmt.Sprintf("@%s requested a review on PR #%d", requestedBy, prNumber),
		URL:         prURL,
		Color:       ColorYellow,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	return ThreadMessage{
		Content: content,
		Embeds:  []Embed{embed},
	}
}

// FormatPRMerged 格式化「PR 合併」的訊息
func FormatPRMerged(pr *github.PullRequest, mergedBy string) ThreadMessage {
	embed := Embed{
		Title:       fmt.Sprintf("🎉 PR #%d Merged", pr.Number),
		Description: fmt.Sprintf("**%s** has been merged into `%s`", pr.Title, pr.Base.Ref),
		URL:         pr.HTMLURL,
		Color:       ColorPurple,
		Fields: []EmbedField{
			{
				Name:   "Merged by",
				Value:  fmt.Sprintf("@%s", mergedBy),
				Inline: true,
			},
			{
				Name:   "Changes",
				Value:  fmt.Sprintf("+%d −%d", pr.Additions, pr.Deletions),
				Inline: true,
			},
		},
		Timestamp: time.Now().Format(time.RFC3339),
		Footer: &EmbedFooter{
			Text: "Thread will be archived soon",
		},
	}

	return ThreadMessage{
		Embeds: []Embed{embed},
	}
}

// FormatPRClosed 格式化「PR 關閉（未合併）」的訊息
func FormatPRClosed(pr *github.PullRequest, closedBy string) ThreadMessage {
	embed := Embed{
		Title:       fmt.Sprintf("❌ PR #%d Closed", pr.Number),
		Description: fmt.Sprintf("**%s** was closed without merging", pr.Title),
		URL:         pr.HTMLURL,
		Color:       ColorRed,
		Fields: []EmbedField{
			{
				Name:   "Closed by",
				Value:  fmt.Sprintf("@%s", closedBy),
				Inline: true,
			},
		},
		Timestamp: time.Now().Format(time.RFC3339),
		Footer: &EmbedFooter{
			Text: "Thread will be archived soon",
		},
	}

	return ThreadMessage{
		Embeds: []Embed{embed},
	}
}

// FormatPRUpdated 格式化「PR 更新」的訊息（force push, new commits）
func FormatPRUpdated(pr *github.PullRequest) ThreadMessage {
	embed := Embed{
		Title:       "🔄 PR Updated",
		Description: fmt.Sprintf("New commits pushed to `%s`", pr.Head.Ref),
		URL:         pr.HTMLURL,
		Color:       ColorYellow,
		Fields: []EmbedField{
			{
				Name:   "Changes",
				Value:  fmt.Sprintf("+%d −%d", pr.Additions, pr.Deletions),
				Inline: true,
			},
		},
		Timestamp: pr.UpdatedAt.Format(time.RFC3339),
	}

	return ThreadMessage{
		Embeds: []Embed{embed},
	}
}

// formatReviewState 轉換 review state 成易讀的文字
func formatReviewState(state string) string {
	switch state {
	case "approved":
		return "✅ Approved"
	case "changes_requested":
		return "🔴 Changes Requested"
	case "commented":
		return "💬 Commented"
	default:
		return state
	}
}

// FormatWorkflowRunResult 格式化 CI/CD 結果通知
func FormatWorkflowRunResult(wr *github.WorkflowRun) ThreadMessage {
	var emoji string
	var title string
	var color int

	switch wr.Conclusion {
	case "success":
		emoji = "✅"
		title = fmt.Sprintf("%s CI Passed", emoji)
		color = ColorGreen
	case "failure":
		emoji = "❌"
		title = fmt.Sprintf("%s CI Failed", emoji)
		color = ColorRed
	case "timed_out":
		emoji = "⏰"
		title = fmt.Sprintf("%s CI Timed Out", emoji)
		color = ColorRed
	case "cancelled":
		emoji = "🚫"
		title = fmt.Sprintf("%s CI Cancelled", emoji)
		color = ColorGray
	default:
		emoji = "❓"
		title = fmt.Sprintf("%s CI: %s", emoji, wr.Conclusion)
		color = ColorGray
	}

	commitShort := wr.HeadSHA
	if len(commitShort) > 7 {
		commitShort = commitShort[:7]
	}

	description := fmt.Sprintf("**%s** — Commit `%s`", wr.Name, commitShort)

	embed := Embed{
		Title:       title,
		Description: description,
		URL:         wr.HTMLURL,
		Color:       color,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	return ThreadMessage{
		Embeds: []Embed{embed},
	}
}

// FormatThreadTitle 格式化 thread 標題（限制 100 字元）
func FormatThreadTitle(prNumber int, prTitle string) string {
	title := fmt.Sprintf("PR #%d: %s", prNumber, prTitle)

	// Discord forum thread title 限制 100 字元
	if len(title) > 100 {
		return title[:97] + "..."
	}

	return title
}
