package xapi

import (
	"context"
	"encoding/json"
	"strings"
)

// List is an X list.
//
// MemberCount is a pointer for the same reason ListedUser's counts are: X omits
// it for some lists, and bird prints a 0 only when X actually reported one.
type List struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Description is a pointer for the same reason ListedUser's is: bird passes
	// listResult.description straight through, so an empty description is
	// emitted as "" and only a missing key omits it.
	Description     *string    `json:"description,omitempty"`
	MemberCount     *int       `json:"memberCount,omitempty"`
	SubscriberCount *int       `json:"subscriberCount,omitempty"`
	IsPrivate       bool       `json:"isPrivate"`
	CreatedAt       int64      `json:"createdAt,omitempty"`
	Owner           *ListOwner `json:"owner,omitempty"`
}

// ListOwner is the account a list belongs to.
type ListOwner struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// URL returns the list's permalink.
func (l List) URL() string {
	return "https://x.com/i/lists/" + l.ID
}

// OwnedLists returns lists the authenticated account owns.
func (c *Client) OwnedLists(ctx context.Context, count int) ([]List, error) {
	return c.lists(ctx, "ListOwnerships", count)
}

// ListMemberships returns lists the authenticated account belongs to.
func (c *Client) ListMemberships(ctx context.Context, count int) ([]List, error) {
	return c.lists(ctx, "ListMemberships", count)
}

func (c *Client) lists(ctx context.Context, operation string, count int) ([]List, error) {
	viewer, err := c.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}

	variables := map[string]any{
		"userId":                   viewer.ID,
		"count":                    count,
		"isListMembershipShown":    true,
		"isListMemberTargetUserId": viewer.ID,
	}
	body, err := c.graphQL(ctx, operation, variables, listsFeatures, nil)
	if err != nil {
		return nil, err
	}
	return parseLists(body)
}

type listEntry struct {
	Content struct {
		ItemContent struct {
			List *rawList `json:"list"`
		} `json:"itemContent"`
	} `json:"content"`
}

type rawList struct {
	IDStr           string  `json:"id_str"`
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	MemberCount     *int    `json:"member_count"`
	SubscriberCount *int    `json:"subscriber_count"`
	Mode            string  `json:"mode"`
	CreatedAt       int64   `json:"created_at"`
	UserResults     *struct {
		Result *rawUser `json:"result"`
	} `json:"user_results"`
}

func parseLists(body []byte) ([]List, error) {
	var resp struct {
		Data struct {
			User struct {
				Result struct {
					Timeline struct {
						Timeline struct {
							Instructions []struct {
								Entries []listEntry `json:"entries"`
							} `json:"instructions"`
						} `json:"timeline"`
					} `json:"timeline"`
				} `json:"result"`
			} `json:"user"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &APIError{Message: "decoding lists response: " + err.Error()}
	}

	instructions := resp.Data.User.Result.Timeline.Timeline.Instructions
	if len(instructions) == 0 && len(resp.Errors) > 0 {
		messages := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return nil, &APIError{Message: joinMessages(messages)}
	}

	var lists []List
	for _, instruction := range instructions {
		for _, entry := range instruction.Entries {
			list, ok := mapList(entry.Content.ItemContent.List)
			if !ok {
				continue
			}
			lists = append(lists, list)
		}
	}
	return lists, nil
}

func mapList(raw *rawList) (List, bool) {
	if raw == nil || raw.IDStr == "" || raw.Name == "" {
		return List{}, false
	}

	list := List{
		ID:              raw.IDStr,
		Name:            raw.Name,
		Description:     raw.Description,
		MemberCount:     raw.MemberCount,
		SubscriberCount: raw.SubscriberCount,
		IsPrivate:       strings.EqualFold(raw.Mode, "private"),
		CreatedAt:       raw.CreatedAt,
	}
	if raw.UserResults != nil {
		if owner, ok := mapUser(raw.UserResults.Result.unwrap()); ok {
			list.Owner = &ListOwner{ID: owner.ID, Username: owner.Username, Name: owner.Name}
		}
	}
	return list, true
}
