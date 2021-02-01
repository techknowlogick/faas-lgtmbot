package function

import (
	"code.gitea.io/sdk/gitea"
	"io/ioutil"
	"net/http"
	"strings"

	scm "github.com/jenkins-x/go-scm/scm"
	giteaWebhook "github.com/jenkins-x/go-scm/scm/driver/gitea"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	webhookService := giteaWebhook.NewWebHookService()
	payload, err := webhookService.Parse(r, getWebhookSecret)
	if err != nil {
		// webhook failed to parse, either due to invalid secret or other reason
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	owner := ""
	repo := ""
	index := int64(0)
	// validate that we received a PR Hook
	switch v := payload.(type) {
	case *scm.PullRequestHook:
		owner = v.Repo.Namespace
		repo = v.Repo.Name
		index = int64(v.PullRequest.Number)
	default:
		// unexpected hook passed
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if index == 0 {
		// unexpected hook passed, PR should have an index
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// get gitea secrets & setup client
	giteaHost, err := getAPISecret("gitea-host")
	if err != nil {
		// failed to get secret
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	giteaToken, err := getAPISecret("gitea-token")
	if err != nil {
		// failed to get secret
		w.WriteHeader(http.StatusInternalServerError)
	}
	giteaClient, err := gitea.NewClient(string(giteaHost), gitea.SetToken(string(giteaToken)))
	if err != nil {
		// failed to setup gitea client
		w.WriteHeader(http.StatusInternalServerError)
	}

	// fetch PR and approvals
	pr, _, err := giteaClient.GetPullRequest(owner, repo, index)
	if err != nil {
		// failed to fetch PR
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	approvals, _, err := giteaClient.ListPullReviews(owner, repo, index, gitea.ListPullReviewsOptions{})
	if err != nil {
		// failed to fetch approvals
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// determine which LGTM label should be used
	approvalCount := 0
	for _, approval := range approvals {
		if approval.State == gitea.ReviewStateApproved {
			approvalCount++
		}
	}
	labelNeeded := "lgtm/done"
	switch approvalCount {
	case 0:
		labelNeeded = "lgtm/need 2"
	case 1:
		labelNeeded = "lgtm/need 1"
	}

	// loop thourgh existing labels to determine if an update is needed
	needUpdate := true
	for _, label := range pr.Labels {
		if !strings.HasPrefix(label.Name, "lgtm/") {
			continue
		}
		if label.Name == labelNeeded {
			needUpdate = false
			continue
		}
		// if label starts with "lgtm/" but isn't the correct label
		giteaClient.DeleteIssueLabel(owner, repo, index, label.ID)
	}
	if !needUpdate {
		// no label changes required
		w.WriteHeader(http.StatusOK)
		return
	}

	// if needed label not set, then set it
	// fetch ID of labelNeeded
	giteaLabels, _, err := giteaClient.ListRepoLabels(owner, repo, gitea.ListLabelsOptions{})
	if err != nil {
		// failed to fetch labels
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	labelID := int64(0)
	for _, label := range giteaLabels {
		if label.Name == labelNeeded {
			labelID = label.ID
		}
	}
	if labelID == 0 {
		// failed to find label, TODO: create label
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// set label on PR
	createSlice := []int64{int64(labelID)}
	_, _, err = giteaClient.AddIssueLabels(owner, repo, index, gitea.IssueLabelsOption{createSlice})
	if err != nil {
		// failed to set label
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// all fine
	w.WriteHeader(http.StatusOK)
	return
}

func getWebhookSecret(scm.Webhook) (string, error) {
	secret, err := getAPISecret("webhook-secret")
	return string(secret), err
}

func getAPISecret(secretName string) (secretBytes []byte, err error) {
	// read from the openfaas secrets folder
	return ioutil.ReadFile("/var/openfaas/secrets/" + secretName)
}
