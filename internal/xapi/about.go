package xapi

import (
	"context"
	"encoding/json"
	"fmt"
)

// AboutProfile is X's "About this account" panel: where the account is based
// and how much X trusts that signal.
//
// Every field is optional. X omits the whole panel for many accounts, and omits
// individual fields for many more, so a zero value means "not reported" rather
// than a default. The pointers on the two booleans preserve that distinction —
// "location accurate: No" and "location accuracy not reported" are different
// answers and bird prints only the former.
type AboutProfile struct {
	AccountBasedIn         string `json:"accountBasedIn,omitempty"`
	Source                 string `json:"source,omitempty"`
	CreatedCountryAccurate *bool  `json:"createdCountryAccurate,omitempty"`
	LocationAccurate       *bool  `json:"locationAccurate,omitempty"`
	LearnMoreURL           string `json:"learnMoreUrl,omitempty"`
}

// AboutAccount returns account origin and location information for a handle.
func (c *Client) AboutAccount(ctx context.Context, handle string) (*AboutProfile, error) {
	name := NormalizeHandle(handle)
	if name == "" {
		return nil, fmt.Errorf("x api: empty username")
	}

	// No features or fieldToggles: AboutAccountQuery rejects the request when
	// either is present.
	body, err := c.graphQL(ctx,
		"AboutAccountQuery",
		map[string]any{"screenName": name},
		nil,
		nil,
	)
	if err != nil {
		return nil, err
	}
	return parseAboutProfile(body)
}

type aboutResponse struct {
	Data struct {
		UserResult struct {
			Result struct {
				AboutProfile *struct {
					AccountBasedIn         string `json:"account_based_in"`
					Source                 string `json:"source"`
					CreatedCountryAccurate *bool  `json:"created_country_accurate"`
					LocationAccurate       *bool  `json:"location_accurate"`
					LearnMoreURL           string `json:"learn_more_url"`
				} `json:"about_profile"`
			} `json:"result"`
		} `json:"user_result_by_screen_name"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func parseAboutProfile(body []byte) (*AboutProfile, error) {
	var resp aboutResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &APIError{Message: "decoding about response: " + err.Error()}
	}

	if resp.Data.UserResult.Result.AboutProfile == nil {
		if len(resp.Errors) > 0 {
			messages := make([]string, 0, len(resp.Errors))
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return nil, &APIError{Message: joinMessages(messages)}
		}
		// X answers 200 with no panel for accounts that have none. Treat it as
		// an error so the caller reports it rather than printing a bare header.
		return nil, &APIError{Message: "missing about_profile in response"}
	}

	raw := resp.Data.UserResult.Result.AboutProfile
	return &AboutProfile{
		AccountBasedIn:         raw.AccountBasedIn,
		Source:                 raw.Source,
		CreatedCountryAccurate: raw.CreatedCountryAccurate,
		LocationAccurate:       raw.LocationAccurate,
		LearnMoreURL:           raw.LearnMoreURL,
	}, nil
}

func joinMessages(messages []string) string {
	out := ""
	for i, m := range messages {
		if i > 0 {
			out += ", "
		}
		out += m
	}
	return out
}
