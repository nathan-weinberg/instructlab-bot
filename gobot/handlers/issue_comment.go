package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/google/go-github/v60/github"
	"github.com/palantir/go-githubapp/githubapp"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type PRCommentHandler struct {
	githubapp.ClientCreator
	Logger        *zap.SugaredLogger
	RedisHostPort string
	RequiredLabel string
}

type PRComment struct {
	repoOwner string
	repoName  string
	prNum     int
	author    string
	body      string
	installID int64
}

func (h *PRCommentHandler) Handles() []string {
	return []string{"issue_comment"}
}

func (h *PRCommentHandler) Handle(ctx context.Context, eventType, deliveryID string, payload []byte) error {
	var event github.IssueCommentEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return errors.Wrap(err, "failed to parse issue comment event payload")
	}

	if !event.GetIssue().IsPullRequest() {
		return nil
	}

	if event.GetAction() != "created" {
		return nil
	}

	repo := event.GetRepo()
	prComment := PRComment{
		repoOwner: repo.GetOwner().GetLogin(),
		repoName:  repo.GetName(),
		prNum:     event.GetIssue().GetNumber(),
		author:    event.GetComment().GetUser().GetLogin(),
		body:      event.GetComment().GetBody(),
		installID: githubapp.GetInstallationIDFromEvent(&event),
	}

	client, err := h.NewInstallationClient(prComment.installID)
	if err != nil {
		return err
	}

	words := strings.Fields(strings.TrimSpace(prComment.body))
	if len(words) < 2 {
		return nil
	}
	if words[0] != "@instruct-lab-bot" {
		return nil
	}
	switch words[1] {
	case "generate":
		err = h.generateCommand(ctx, client, &prComment)
		if err != nil {
			h.reportError(ctx, client, &prComment, err)
		}
		return err
	default:
		return h.unknownCommand(ctx, client, &prComment)
	}
}

func (h *PRCommentHandler) reportError(ctx context.Context, client *github.Client, prComment *PRComment, err error) {
	h.Logger.Errorf("Error processing command on %s/%s#%d by %s: %v",
		prComment.repoOwner, prComment.repoName, prComment.prNum, prComment.author, err)

	msg := "Beep, boop 🤖  Sorry, An error has occurred."
	botComment := github.IssueComment{
		Body: &msg,
	}

	if _, _, err := client.Issues.CreateComment(ctx, prComment.repoOwner, prComment.repoName, prComment.prNum, &botComment); err != nil {
		h.Logger.Error("Failed to comment on pull request: %w", err)
	}
}

func (h *PRCommentHandler) checkRequiredLabel(ctx context.Context, client *github.Client, prComment *PRComment, requiredLabel string) (bool, error) {
	if requiredLabel == "" {
		return true, nil
	}

	pr, _, err := client.PullRequests.Get(ctx, prComment.repoOwner, prComment.repoName, prComment.prNum)
	if err != nil {
		return false, err
	}

	labelFound := false
	for _, label := range pr.Labels {
		if label.GetName() == requiredLabel {
			labelFound = true
			break
		}
	}

	if !labelFound {
		h.Logger.Infof("Required label %s not found on PR %s/%s#%d by %s",
			requiredLabel, prComment.repoOwner, prComment.repoName, prComment.prNum, prComment.author)
		missingLabelComment := fmt.Sprintf("Beep, boop 🤖: To proceed, the pull request must have the '%s' label.", requiredLabel)
		botComment := github.IssueComment{Body: &missingLabelComment}
		_, _, err = client.Issues.CreateComment(ctx, prComment.repoOwner, prComment.repoName, prComment.prNum, &botComment)
		if err != nil {
			h.Logger.Errorf("Failed to comment on pull request about missing label: %v", err)
		}
		return false, nil
	}

	return true, nil
}

func (h *PRCommentHandler) generateCommand(ctx context.Context, client *github.Client, prComment *PRComment) error {
	h.Logger.Infof("Generate command received on %s/%s#%d by %s",
		prComment.repoOwner, prComment.repoName, prComment.prNum, prComment.author)

	// Check if the required label is present if a required label is in the config file
	present, err := h.checkRequiredLabel(ctx, client, prComment, h.RequiredLabel)
	if !present || err != nil {
		return err
	}

	r := redis.NewClient(&redis.Options{
		Addr:     h.RedisHostPort,
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	jobNumber, err := r.Incr(ctx, "jobs").Result()
	if err != nil {
		return err
	}

	err = r.Set(ctx, "jobs:"+strconv.FormatInt(jobNumber, 10)+":pr_number", prComment.prNum, 0).Err()
	if err != nil {
		return err
	}

	err = r.Set(ctx, "jobs:"+strconv.FormatInt(jobNumber, 10)+":installation_id", prComment.installID, 0).Err()
	if err != nil {
		return err
	}

	err = r.Set(ctx, "jobs:"+strconv.FormatInt(jobNumber, 10)+":repo_owner", prComment.repoOwner, 0).Err()
	if err != nil {
		return err
	}

	err = r.Set(ctx, "jobs:"+strconv.FormatInt(jobNumber, 10)+":repo_name", prComment.repoName, 0).Err()
	if err != nil {
		return err
	}

	err = r.LPush(ctx, "generate", strconv.FormatInt(jobNumber, 10)).Err()
	if err != nil {
		return err
	}
	msg := "Beep, boop 🤖  Generating test data for your pull request.\n\n" +
		"This will take several minutes...\n\n" +
		"Your job ID is " + strconv.FormatInt(jobNumber, 10) + "."
	botComment := github.IssueComment{
		Body: &msg,
	}

	if _, _, err := client.Issues.CreateComment(ctx, prComment.repoOwner, prComment.repoName, prComment.prNum, &botComment); err != nil {
		h.Logger.Error("Failed to comment on pull request: %w", err)
		return err
	}

	return nil
}

func (h *PRCommentHandler) unknownCommand(ctx context.Context, client *github.Client, prComment *PRComment) error {
	h.Logger.Infof("Unknown command received on %s/%s#%d by %s",
		prComment.repoOwner, prComment.repoName, prComment.prNum, prComment.author)

	msg := "Beep, boop 🤖  Sorry, I don't understand that command"
	botComment := github.IssueComment{
		Body: &msg,
	}

	if _, _, err := client.Issues.CreateComment(ctx, prComment.repoOwner, prComment.repoName, prComment.prNum, &botComment); err != nil {
		h.Logger.Error("Failed to comment on pull request: %w", err)
		return err
	}

	return nil
}
