package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type greenhopperBacklogData struct {
	Issues     []greenhopperIssue `json:"issues"`
	Sprints    []greenhopperSprint `json:"sprints"`
	EntityData struct {
		Statuses map[string]greenhopperStatusMeta `json:"statuses"`
		Types    map[string]greenhopperTypeMeta   `json:"types"`
	} `json:"entityData"`
}

type greenhopperSprint struct {
	ID           int   `json:"id"`
	Sequence     int   `json:"sequence"`
	RapidViewID  int   `json:"rapidViewId"`
	Name         string `json:"name"`
	State        string `json:"state"`
	StartDate    string `json:"startDate"`
	EndDate      string `json:"endDate"`
	CompleteDate string `json:"completeDate"`
	IssuesIDs    []int  `json:"issuesIds"`
}

type greenhopperIssue struct {
	ID           int    `json:"id"`
	Key          string `json:"key"`
	Summary      string `json:"summary"`
	AssigneeName string `json:"assigneeName"`
	StatusID     string `json:"statusId"`
	TypeID       string `json:"typeId"`
}

type greenhopperStatusMeta struct {
	StatusName string `json:"statusName"`
	Status     struct {
		Name string `json:"name"`
	} `json:"status"`
}

type greenhopperTypeMeta struct {
	TypeName string `json:"typeName"`
}

func (c *Client) GreenhopperSprints(boardID int, qp string) ([]*Sprint, error) {
	data, err := c.greenhopperBacklogData(boardID)
	if err != nil {
		return nil, err
	}

	allowed := allowedSprintStates(qp)
	out := make([]*Sprint, 0, len(data.Sprints))
	for _, sprint := range data.Sprints {
		mapped := mapGreenhopperSprint(sprint)
		if len(allowed) > 0 {
			if _, ok := allowed[mapped.Status]; !ok {
				continue
			}
		}
		out = append(out, mapped)
	}

	return out, nil
}

func (c *Client) GreenhopperSprintIssuesByState(boardID int, state string) (*Sprint, []*Issue, error) {
	data, err := c.greenhopperBacklogData(boardID)
	if err != nil {
		return nil, nil, err
	}

	sprint := selectGreenhopperSprint(data.Sprints, strings.ToLower(state))
	if sprint == nil {
		return nil, nil, ErrNoResult
	}

	mappedSprint := mapGreenhopperSprint(*sprint)
	issues := mapGreenhopperIssues(*sprint, data)

	return mappedSprint, issues, nil
}

func (c *Client) GreenhopperSprintIssuesByID(boardID, sprintID int) (*Sprint, []*Issue, error) {
	data, err := c.greenhopperBacklogData(boardID)
	if err != nil {
		return nil, nil, err
	}

	for _, sprint := range data.Sprints {
		if sprint.ID == sprintID {
			mappedSprint := mapGreenhopperSprint(sprint)
			issues := mapGreenhopperIssues(sprint, data)
			return mappedSprint, issues, nil
		}
	}

	return nil, nil, ErrNoResult
}

func (c *Client) greenhopperBacklogData(boardID int) (*greenhopperBacklogData, error) {
	boardPage := fmt.Sprintf("%s/secure/RapidBoard.jspa?rapidView=%d&view=planning&issueLimit=100", c.server, boardID)
	path := fmt.Sprintf("%s/rest/greenhopper/1.0/xboard/plan/backlog/data.json?rapidViewId=%d", c.server, boardID)

	res, err := c.request(context.Background(), http.MethodGet, path, nil, Header{
		"Referer": boardPage,
		"Accept":  "*/*",
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, ErrEmptyResponse
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, formatUnexpectedResponse(res)
	}

	var out greenhopperBacklogData
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}

	return &out, nil
}

func allowedSprintStates(qp string) map[string]struct{} {
	const prefix = "state="
	if !strings.HasPrefix(qp, prefix) {
		return nil
	}

	states := make(map[string]struct{})
	for _, state := range strings.Split(strings.TrimPrefix(qp, prefix), ",") {
		state = strings.TrimSpace(strings.ToLower(state))
		if state == "" {
			continue
		}
		states[state] = struct{}{}
	}

	return states
}

func mapGreenhopperSprint(s greenhopperSprint) *Sprint {
	return &Sprint{
		ID:           s.ID,
		Name:         s.Name,
		Status:       strings.ToLower(s.State),
		StartDate:    normalizeGreenhopperDate(s.StartDate),
		EndDate:      normalizeGreenhopperDate(s.EndDate),
		CompleteDate: normalizeGreenhopperDate(s.CompleteDate),
		BoardID:      s.RapidViewID,
	}
}

func normalizeGreenhopperDate(in string) string {
	if in == "" {
		return ""
	}
	parsed, err := time.Parse("02/Jan/06 3:04 PM", in)
	if err != nil {
		return in
	}
	return parsed.Format(time.RFC3339)
}

func selectGreenhopperSprint(sprints []greenhopperSprint, desiredState string) *greenhopperSprint {
	matches := make([]greenhopperSprint, 0)
	for _, sprint := range sprints {
		if strings.ToLower(sprint.State) == desiredState {
			matches = append(matches, sprint)
		}
	}
	if len(matches) == 0 {
		return nil
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Sequence < matches[j].Sequence
	})

	switch desiredState {
	case SprintStateClosed:
		return &matches[len(matches)-1]
	default:
		return &matches[0]
	}
}

func mapGreenhopperIssues(sprint greenhopperSprint, data *greenhopperBacklogData) []*Issue {
	issueByID := make(map[int]greenhopperIssue, len(data.Issues))
	for _, issue := range data.Issues {
		issueByID[issue.ID] = issue
	}

	issues := make([]*Issue, 0, len(sprint.IssuesIDs))
	for _, id := range sprint.IssuesIDs {
		ghIssue, ok := issueByID[id]
		if !ok {
			continue
		}
		issues = append(issues, mapGreenhopperIssue(ghIssue, data))
	}

	return issues
}

func mapGreenhopperIssue(issue greenhopperIssue, data *greenhopperBacklogData) *Issue {
	out := &Issue{Key: issue.Key}
	out.Fields.Summary = issue.Summary
	out.Fields.Assignee.Name = issue.AssigneeName

	if status, ok := data.EntityData.Statuses[issue.StatusID]; ok {
		if status.Status.Name != "" {
			out.Fields.Status.Name = status.Status.Name
		} else {
			out.Fields.Status.Name = status.StatusName
		}
	}
	if typeMeta, ok := data.EntityData.Types[issue.TypeID]; ok {
		out.Fields.IssueType.Name = typeMeta.TypeName
	}

	return out
}
