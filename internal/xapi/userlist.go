package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ListedUser is a user as it appears in a followers/following listing.
//
// This is deliberately not xapi.User. A listing carries different presence
// semantics — X omits counts for suspended and some protected accounts, and
// "0 followers" is a different answer from "not reported" — so the optional
// fields are pointers. The JSON tags match bird's listing payload exactly,
// which is a different shape from the profile lookup's.
type ListedUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	// Description is a pointer for the same reason the counts are. bird reads
	// legacy.description, a string that is present-but-empty for most accounts
	// (12 of 15 in a live sample) and genuinely absent only on a core-only
	// payload. A plain string with omitempty collapsed those two cases and
	// dropped `"description": ""` from every such user.
	Description     *string `json:"description,omitempty"`
	FollowersCount  *int    `json:"followersCount,omitempty"`
	FollowingCount  *int    `json:"followingCount,omitempty"`
	IsBlueVerified  *bool   `json:"isBlueVerified,omitempty"`
	ProfileImageURL string  `json:"profileImageUrl,omitempty"`
	CreatedAt       string  `json:"createdAt,omitempty"`
	// Unavailable marks a listing member X refused to render — suspended,
	// deactivated, or hidden from this viewer. Only ID is populated. It is a
	// real member of the list, so a follow-graph consumer must count it as
	// present; it is just not describable.
	Unavailable bool `json:"unavailable,omitempty"`
}

// UserListPage is one page of a user-list timeline (followers, following).
//
// NextCursor is empty at the end of the list. X signals that by omitting the
// bottom cursor entry, not by repeating the last one.
type UserListPage struct {
	Users      []ListedUser
	NextCursor string
}

// Following returns accounts the given user id follows.
func (c *Client) Following(ctx context.Context, userID string, count int, cursor string) (*UserListPage, error) {
	page, err := c.userList(ctx, "Following", userID, count, cursor)
	if err == nil {
		return page, nil
	}
	if IsRateLimited(err) {
		return nil, err
	}
	if !isStaleQueryID(err) {
		return nil, err
	}
	return c.userListViaREST(ctx, c.restPaths(followingRESTPaths), userID, count, cursor)
}

// Followers returns accounts following the given user id.
//
// In practice this always takes the REST path: see the note on
// followersRESTPaths.
func (c *Client) Followers(ctx context.Context, userID string, count int, cursor string) (*UserListPage, error) {
	page, err := c.userList(ctx, "Followers", userID, count, cursor)
	if err == nil {
		return page, nil
	}
	if IsRateLimited(err) {
		return nil, err
	}
	if !isStaleQueryID(err) {
		return nil, err
	}
	return c.userListViaREST(ctx, c.restPaths(followersRESTPaths), userID, count, cursor)
}

// restPaths lets tests redirect the v1.1 endpoints at a local server.
func (c *Client) restPaths(defaults []string) []string {
	if c.userListRESTPaths != nil {
		return c.userListRESTPaths
	}
	return defaults
}

func (c *Client) userList(ctx context.Context, operation, userID string, count int, cursor string) (*UserListPage, error) {
	variables := map[string]any{
		"userId":                 userID,
		"count":                  clampCount(count),
		"includePromotedContent": false,
	}
	// X rejects an explicit null cursor, so the key is only present when set.
	if cursor != "" {
		variables["cursor"] = cursor
	}

	body, err := c.graphQL(ctx, operation, variables, followingFeatures, nil)
	if err != nil {
		return nil, err
	}
	return parseUserList(body)
}

// userTimelineResponse covers the two roots X uses for these operations.
type userTimelineResponse struct {
	Data struct {
		User struct {
			Result struct {
				Timeline struct {
					Timeline *userInstructions `json:"timeline"`
				} `json:"timeline"`
				// Newer payloads drop the doubled "timeline" nesting.
				TimelineDirect *userInstructions `json:"timeline_v2"`
			} `json:"result"`
		} `json:"user"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type userInstructions struct {
	Instructions *[]userInstruction `json:"instructions"`
}

type userInstruction struct {
	TypeName     string `json:"__typename"`
	Type         string `json:"type"`
	typeShapeErr string
	rawObject    map[string]json.RawMessage
	Entries      *[]userEntry `json:"entries"`
	Entry        *userEntry   `json:"entry"`
}

func (instruction *userInstruction) UnmarshalJSON(data []byte) error {
	type alias userInstruction
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*instruction = userInstruction(decoded)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	instruction.rawObject = object
	for _, key := range []string{"type", "__typename"} {
		raw, present := object[key]
		if !present {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || value == "" {
			instruction.typeShapeErr = fmt.Sprintf("%s is present but not a nonempty string", key)
		}
	}
	return nil
}

type userEntry struct {
	// EntryID is the timeline entry key, "user-<rest_id>" for a listing member.
	// It is the only place the id of an unavailable member survives, so it is
	// read rather than ignored.
	EntryID string            `json:"entryId"`
	Content *userEntryContent `json:"content"`
}

type userEntryContent struct {
	TypeName    string            `json:"__typename"`
	EntryType   string            `json:"entryType"`
	CursorType  string            `json:"cursorType"`
	Value       string            `json:"value"`
	ItemContent *userItemContent  `json:"itemContent"`
	Items       *[]userModuleItem `json:"items"`
}

type userItemContent struct {
	TypeName    string `json:"__typename"`
	ItemType    string `json:"itemType"`
	UserResults *struct {
		Result *rawUser `json:"result"`
	} `json:"user_results"`
}

type userModuleItem struct {
	ItemContent *userItemContent `json:"itemContent"`
	Item        *struct {
		ItemContent *userItemContent `json:"itemContent"`
	} `json:"item"`
	Content *struct {
		ItemContent *userItemContent `json:"itemContent"`
	} `json:"content"`
}

// rawUser mirrors the shape X returns for a user entry. Identity moved from
// legacy to core in newer payloads and both still appear, so each field falls
// back across the two.
type rawUser struct {
	TypeName       string `json:"__typename"`
	RestID         string `json:"rest_id"`
	IsBlueVerified *bool  `json:"is_blue_verified"`
	// UserWithVisibilityResults wraps the real user one level deeper.
	User   *rawUser `json:"user"`
	Legacy *struct {
		ScreenName string `json:"screen_name"`
		Name       string `json:"name"`
		// A pointer so a legacy block without the key stays absent rather than
		// becoming "", which bird distinguishes.
		Description          *string `json:"description"`
		FollowersCount       *int    `json:"followers_count"`
		FriendsCount         *int    `json:"friends_count"`
		StatusesCount        int     `json:"statuses_count"`
		Verified             bool    `json:"verified"`
		CreatedAt            string  `json:"created_at"`
		ProfileImageURLHTTPS string  `json:"profile_image_url_https"`
	} `json:"legacy"`
	Core *struct {
		ScreenName string `json:"screen_name"`
		Name       string `json:"name"`
		CreatedAt  string `json:"created_at"`
	} `json:"core"`
	Avatar *struct {
		ImageURL string `json:"image_url"`
	} `json:"avatar"`
}

// unwrap resolves the UserWithVisibilityResults wrapper.
func (r *rawUser) unwrap() *rawUser {
	if r != nil && r.TypeName == "UserWithVisibilityResults" && r.User != nil {
		return r.User
	}
	return r
}

func parseUserList(body []byte) (*UserListPage, error) {
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &APIError{Message: "decoding user list response: " + err.Error()}
	}
	if len(resp.Errors) > 0 {
		messages := make([]string, 0, len(resp.Errors))
		for _, item := range resp.Errors {
			messages = append(messages, item.Message)
		}
		return nil, &APIError{Message: joinMessages(messages)}
	}

	var timeline userInstructions
	found := false
	for _, root := range [][]string{{"user", "result", "timeline", "timeline"}, {"user", "result", "timeline_v2"}} {
		raw, present, err := navigateStrict(resp.Data, root)
		if err != nil {
			return nil, &APIError{Message: "malformed user-list root: " + err.Error()}
		}
		if !present {
			continue
		}
		if string(raw) == "null" || json.Unmarshal(raw, &timeline) != nil {
			return nil, &APIError{Message: "malformed user-list timeline root"}
		}
		found = true
		break
	}
	if !found || timeline.Instructions == nil {
		return nil, &APIError{Message: "unrecognized user-list response shape: missing instructions collection"}
	}

	page := &UserListPage{}
	for _, instruction := range *timeline.Instructions {
		if instruction.typeShapeErr != "" {
			return nil, &APIError{Message: "malformed user-list instruction discriminator: " + instruction.typeShapeErr}
		}
		kind, err := consistentDiscriminator(instruction.Type, instruction.TypeName, "user-list instruction")
		if err != nil {
			return nil, err
		}
		if instruction.Entries == nil {
			if instruction.Entry == nil && (kind == "TimelineClearCache" || kind == "TimelineTerminateTimeline") {
				if err := validateNonDataInstructionKeys(instruction.rawObject, kind); err != nil {
					return nil, err
				}
				continue // observed live non-data cache/termination instructions
			}
			return nil, &APIError{Message: fmt.Sprintf("unsupported user-list instruction %q: expected entries collection", firstNonEmpty(instruction.Type, instruction.TypeName))}
		}
		if instruction.Entry != nil {
			return nil, &APIError{Message: fmt.Sprintf("unsupported singular user-list instruction %q", kind)}
		}
		if kind != "" && kind != "TimelineAddEntries" {
			return nil, &APIError{Message: fmt.Sprintf("unsupported user-list instruction type %q", kind)}
		}
		for _, entry := range *instruction.Entries {
			if entry.Content == nil {
				return nil, &APIError{Message: "malformed user-list entry: missing content"}
			}
			contentType, err := consistentDiscriminator(entry.Content.EntryType, entry.Content.TypeName, "user-list entry content")
			if err != nil {
				return nil, err
			}
			hasData := entry.Content.ItemContent != nil || entry.Content.Items != nil
			typedCursor := contentType == "TimelineTimelineCursor"
			if typedCursor && entry.Content.CursorType == "" {
				return nil, &APIError{Message: "malformed typed user-list cursor: missing cursorType"}
			}
			if entry.Content.CursorType != "" && hasData {
				return nil, &APIError{Message: "malformed user-list entry: cursor also carries data"}
			}
			switch entry.Content.CursorType {
			case "Bottom":
				if entry.Content.Value == "" {
					return nil, &APIError{Message: "malformed user-list Bottom cursor: empty value"}
				}
				if isTerminalUserListCursor(entry.Content.Value) {
					continue
				}
				if page.NextCursor != "" && page.NextCursor != entry.Content.Value {
					return nil, &APIError{Message: "conflicting user-list Bottom cursors"}
				}
				page.NextCursor = entry.Content.Value
				continue
			case "Top":
				if entry.Content.Value == "" {
					return nil, &APIError{Message: "malformed user-list Top cursor: empty value"}
				}
				continue
			case "":
			default:
				return nil, &APIError{Message: fmt.Sprintf("unsupported user-list cursor type %q", entry.Content.CursorType)}
			}
			users, err := usersFromEntry(entry)
			if err != nil {
				return nil, err
			}
			page.Users = append(page.Users, users...)
		}
	}
	return page, nil
}

func usersFromEntry(entry userEntry) ([]ListedUser, error) {
	if entry.Content == nil {
		return nil, &APIError{Message: "malformed user-list entry: missing content"}
	}
	kind, err := consistentDiscriminator(entry.Content.EntryType, entry.Content.TypeName, "user-list entry content")
	if err != nil {
		return nil, err
	}
	if kind != "TimelineTimelineModule" {
		user, ok, err := mapUserEntry(entry)
		if err != nil || !ok {
			return nil, err
		}
		return []ListedUser{user}, nil
	}
	if entry.Content.ItemContent != nil {
		return nil, &APIError{Message: "malformed user-list module: mixed direct itemContent and items"}
	}

	if entry.Content.Items == nil {
		return nil, &APIError{Message: "malformed user-list module: missing nested items"}
	}
	if len(*entry.Content.Items) == 0 {
		return nil, nil // explicitly present-empty is a provably empty decoration
	}
	var users []ListedUser
	for index, moduleItem := range *entry.Content.Items {
		var contents []*userItemContent
		if moduleItem.ItemContent != nil {
			contents = append(contents, moduleItem.ItemContent)
		}
		if moduleItem.Item != nil && moduleItem.Item.ItemContent != nil {
			contents = append(contents, moduleItem.Item.ItemContent)
		}
		if moduleItem.Content != nil && moduleItem.Content.ItemContent != nil {
			contents = append(contents, moduleItem.Content.ItemContent)
		}
		if len(contents) == 0 {
			return nil, &APIError{Message: fmt.Sprintf("malformed user-list module item %d: no item content", index)}
		}
		for _, content := range contents {
			nested := userEntry{Content: &userEntryContent{ItemContent: content}}
			user, ok, err := mapUserEntry(nested)
			if err != nil {
				return nil, err
			}
			if ok {
				users = append(users, user)
			}
		}
	}
	return users, nil
}

func mapUserEntry(entry userEntry) (ListedUser, bool, error) {
	if entry.Content == nil {
		return ListedUser{}, false, &APIError{Message: "malformed user-list entry: missing content"}
	}
	item := entry.Content.ItemContent
	if item == nil {
		if isNonUserTimelineType(entry.Content.TypeName) || isNonUserTimelineType(entry.Content.EntryType) {
			return ListedUser{}, false, nil
		}
		return ListedUser{}, false, &APIError{Message: "unrecognized user-list entry: no item content"}
	}
	kind, err := consistentDiscriminator(item.ItemType, item.TypeName, "user-list item content")
	if err != nil {
		return ListedUser{}, false, err
	}
	if item.UserResults == nil {
		if isNonUserTimelineType(kind) {
			return ListedUser{}, false, nil
		}
		return ListedUser{}, false, &APIError{Message: fmt.Sprintf("unrecognized user-list item type %q", kind)}
	}
	raw := item.UserResults.Result
	if raw == nil {
		return ListedUser{}, false, &APIError{Message: "malformed user-list entry: user_results has no result"}
	}
	switch raw.TypeName {
	case "TimelineMessagePrompt":
		return ListedUser{}, false, nil
	case "UserUnavailable", "UserTombstone":
		// X answers with no rest_id for a member it will not render, but the
		// membership itself is real and its id survives in the entry key.
		// Rejecting the page here discarded every other user on it, and
		// dropping the entry silently would read a live account as an
		// unfollow, so the member is returned with the identity X withheld
		// left empty and Unavailable set.
		id := userIDFromEntryID(entry.EntryID)
		if id == "" {
			return ListedUser{}, false, &APIError{Message: fmt.Sprintf("user-list entry %q has no recoverable id", raw.TypeName)}
		}
		return ListedUser{ID: id, Unavailable: true}, true, nil
	case "UserWithVisibilityResults":
		if raw.User == nil || raw.User.TypeName != "User" {
			return ListedUser{}, false, &APIError{Message: "malformed user-list visibility wrapper"}
		}
		raw = raw.User
	case "User":
	default:
		return ListedUser{}, false, &APIError{Message: fmt.Sprintf("unrecognized user-list result type %q", raw.TypeName)}
	}
	user, ok := mapUser(raw)
	if !ok {
		return ListedUser{}, false, &APIError{Message: fmt.Sprintf("malformed user-list entry for id %q", raw.RestID)}
	}
	return user, true, nil
}

// isTerminalUserListCursor reports X's end-of-list sentinel. The v1.1 REST
// listing says "there is nothing after this page" with next_cursor_str "0"; the
// GraphQL listing says the same thing with a Bottom cursor whose leading
// component is "0", as in "0|2087029644107709401".
//
// Following that value does not fail — X answers with an empty page and another
// "0|" cursor whose suffix has changed, so the walk's repeat and no-advance
// guards never fire. Reading it as a real cursor meant a user list never
// reported its end: the walk only stopped at its page cap, which a caller must
// treat as an incomplete snapshot, so a full following list could never be
// reconciled no matter how small the account.
func isTerminalUserListCursor(value string) bool {
	leading, _, separated := strings.Cut(value, "|")
	if !separated {
		return value == "0"
	}
	return leading == "0"
}

// userIDFromEntryID recovers the numeric id X puts in a user-list entry key
// ("user-1234567890"). Nested module items carry no key of their own, so the
// empty string returned for them keeps an ambiguous module entry failing
// closed rather than borrowing a sibling's id.
func userIDFromEntryID(entryID string) string {
	rest, found := strings.CutPrefix(strings.TrimSpace(entryID), "user-")
	if !found || rest == "" {
		return ""
	}
	for _, digit := range rest {
		if digit < '0' || digit > '9' {
			return ""
		}
	}
	return rest
}

func isNonUserTimelineType(typeName string) bool {
	switch typeName {
	case "TimelineMessagePrompt":
		return true
	default:
		return false
	}
}

func mapUser(raw *rawUser) (ListedUser, bool) {
	if raw == nil || raw.TypeName != "User" || raw.RestID == "" {
		return ListedUser{}, false
	}

	user := ListedUser{ID: raw.RestID, IsBlueVerified: raw.IsBlueVerified}

	// The counts come from legacy only. When X sends a core-only payload they
	// are genuinely absent, and the pointers stay nil so the caller can tell.
	if raw.Legacy != nil {
		user.Username = raw.Legacy.ScreenName
		user.Name = raw.Legacy.Name
		user.Description = raw.Legacy.Description
		user.FollowersCount = raw.Legacy.FollowersCount
		user.FollowingCount = raw.Legacy.FriendsCount
		user.ProfileImageURL = raw.Legacy.ProfileImageURLHTTPS
		user.CreatedAt = raw.Legacy.CreatedAt
	}
	if raw.Core != nil {
		if user.Username == "" {
			user.Username = raw.Core.ScreenName
		}
		if user.Name == "" {
			user.Name = raw.Core.Name
		}
		if user.CreatedAt == "" {
			user.CreatedAt = raw.Core.CreatedAt
		}
	}
	if user.ProfileImageURL == "" && raw.Avatar != nil {
		user.ProfileImageURL = raw.Avatar.ImageURL
	}

	if user.Username == "" {
		return ListedUser{}, false
	}
	if user.Name == "" {
		user.Name = user.Username
	}
	return user, true
}

// --- v1.1 REST fallback --------------------------------------------------

// The GraphQL Followers operation returns 404 constantly — every hash birdy
// ships and every hash discovery finds were rejected for all five test
// accounts, with and without a feature set. bird carries the same note:
// "GraphQL Followers regularly returns 404 (queryId churn / endpoint
// flakiness)". The v1.1 list endpoints still answer for a cookie session, and
// are what actually serves the command.
//
// This is the third fallback in this client that looked redundant and turned
// out to be load-bearing, after whoami's settings-page scrape and the friendship
// REST path. The pattern is worth naming: bird's belt-and-braces paths exist
// because X's GraphQL surface is unreliable, not because bird misread its own
// responses.
var (
	followersRESTPaths = []string{
		"https://x.com/i/api/1.1/followers/list.json",
		"https://api.twitter.com/1.1/followers/list.json",
	}
	followingRESTPaths = []string{
		"https://x.com/i/api/1.1/friends/list.json",
		"https://api.twitter.com/1.1/friends/list.json",
	}
)

func (c *Client) userListViaREST(ctx context.Context, endpoints []string, userID string, count int, cursor string) (*UserListPage, error) {
	form := url.Values{
		"user_id":               {userID},
		"count":                 {strconv.Itoa(count)},
		"skip_status":           {"true"},
		"include_user_entities": {"false"},
	}
	if cursor != "" {
		form.Set("cursor", cursor)
	}

	var lastErr error
	for _, base := range endpoints {
		body, err := c.get(ctx, base+"?"+form.Encode())
		if err != nil {
			lastErr = err
			if IsRateLimited(err) {
				return nil, err
			}
			continue
		}

		page, err := parseRESTUserList(body)
		if err != nil {
			return nil, err
		}
		return page, nil
	}
	return nil, lastErr
}

type restUser struct {
	IDStr                string  `json:"id_str"`
	ID                   int64   `json:"id"`
	ScreenName           string  `json:"screen_name"`
	Name                 string  `json:"name"`
	Description          *string `json:"description"`
	FollowersCount       *int    `json:"followers_count"`
	FriendsCount         *int    `json:"friends_count"`
	ProfileImageURLHTTPS string  `json:"profile_image_url_https"`
	CreatedAt            string  `json:"created_at"`
}

func parseRESTUserList(body []byte) (*UserListPage, error) {
	var resp struct {
		Users         []restUser `json:"users"`
		NextCursorStr *string    `json:"next_cursor_str"`
		Errors        []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &APIError{Message: "decoding user list response: " + err.Error()}
	}
	if len(resp.Errors) > 0 {
		messages := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return nil, &APIError{Message: joinMessages(messages)}
	}
	if resp.Users == nil {
		return nil, &APIError{Message: "unrecognized REST user-list response shape: missing users collection"}
	}
	if resp.NextCursorStr == nil {
		return nil, &APIError{Message: "unrecognized REST user-list response shape: missing next_cursor_str"}
	}

	page := &UserListPage{}
	// "0" is X's terminator, not a cursor.
	if *resp.NextCursorStr == "" {
		return nil, &APIError{Message: "malformed REST user-list response: empty next_cursor_str"}
	}
	if *resp.NextCursorStr != "0" {
		page.NextCursor = *resp.NextCursorStr
	}

	for _, u := range resp.Users {
		id := u.IDStr
		if id == "" && u.ID != 0 {
			id = strconv.FormatInt(u.ID, 10)
		}
		if id == "" || u.ScreenName == "" {
			return nil, &APIError{Message: fmt.Sprintf("malformed REST user-list entry: id=%q username=%q", id, u.ScreenName)}
		}
		name := u.Name
		if name == "" {
			name = u.ScreenName
		}
		page.Users = append(page.Users, ListedUser{
			ID:              id,
			Username:        u.ScreenName,
			Name:            name,
			Description:     u.Description,
			FollowersCount:  u.FollowersCount,
			FollowingCount:  u.FriendsCount,
			ProfileImageURL: u.ProfileImageURLHTTPS,
			CreatedAt:       u.CreatedAt,
		})
	}
	return page, nil
}
