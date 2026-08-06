package xapi

// GraphQL endpoint base for X's web client API.
const graphQLBase = "https://x.com/i/api/graphql"

// webBearerToken is X's public web-client bearer token. It is not a secret —
// it ships in x.com's JavaScript bundle and identifies the web app, not the
// user. Per-user authentication comes from the auth_token cookie and the ct0
// CSRF token.
const webBearerToken = "Bearer AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"

// defaultUserAgent mimics a current desktop browser. X rejects requests with a
// non-browser user agent.
const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// tweetDetailQueryIDs are the persisted-query hashes for the TweetDetail
// operation, newest first. X rotates these periodically and serves a 404 for a
// stale one, so the client walks the list. Override with BIRDY_TWEET_DETAIL_QUERY_ID
// when X rotates faster than this list is updated.
var tweetDetailQueryIDs = []string{
	"_NvJCnIjOW__EP5-RF197A",
	"97JF30KziU00483E_8elBA",
}

// tweetDetailFeatures is the feature-flag set X requires on a TweetDetail call.
// X rejects the request outright when a flag it expects is missing, so this
// mirrors the web client's payload rather than trying to send a minimal set.
var tweetDetailFeatures = map[string]bool{
	"articles_preview_enabled":                                                true,
	"articles_rest_api_enabled":                                               true,
	"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
	"communities_web_enable_tweet_community_results_fetch":                    true,
	"creator_subscriptions_quote_tweet_preview_enabled":                       false,
	"creator_subscriptions_tweet_preview_api_enabled":                         true,
	"freedom_of_speech_not_reach_fetch_enabled":                               true,
	"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
	"longform_notetweets_consumption_enabled":                                 true,
	"longform_notetweets_inline_media_enabled":                                true,
	"longform_notetweets_rich_text_read_enabled":                              true,
	"post_ctas_fetch_enabled":                                                 true,
	"premium_content_api_read_enabled":                                        false,
	"profile_label_improvements_pcf_label_in_post_enabled":                    true,
	"responsive_web_edit_tweet_api_enabled":                                   true,
	"responsive_web_enhance_cards_enabled":                                    false,
	"responsive_web_graphql_exclude_directive_enabled":                        true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
	"responsive_web_graphql_timeline_navigation_enabled":                      true,
	"responsive_web_grok_analysis_button_from_backend":                        true,
	"responsive_web_grok_analyze_button_fetch_trends_enabled":                 false,
	"responsive_web_grok_analyze_post_followups_enabled":                      false,
	"responsive_web_grok_annotations_enabled":                                 false,
	"responsive_web_grok_community_note_auto_translation_is_enabled":          false,
	"responsive_web_grok_image_annotation_enabled":                            true,
	"responsive_web_grok_imagine_annotation_enabled":                          true,
	"responsive_web_grok_share_attachment_enabled":                            true,
	"responsive_web_grok_show_grok_translated_post":                           false,
	"responsive_web_jetfuel_frame":                                            true,
	"responsive_web_profile_redirect_enabled":                                 true,
	"responsive_web_twitter_article_plain_text_enabled":                       true,
	"responsive_web_twitter_article_seed_tweet_detail_enabled":                true,
	"responsive_web_twitter_article_seed_tweet_summary_enabled":               true,
	"responsive_web_twitter_article_tweet_consumption_enabled":                true,
	"rweb_tipjar_consumption_enabled":                                         true,
	"rweb_video_screen_enabled":                                               true,
	"rweb_video_timestamps_enabled":                                           true,
	"standardized_nudges_misinfo":                                             true,
	"tweet_awards_web_tipping_enabled":                                        false,
	"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
	"verified_phone_label_enabled":                                            false,
	"view_counts_everywhere_api_enabled":                                      true,
}

// tweetDetailFieldToggles accompanies the feature set on a TweetDetail call.
var tweetDetailFieldToggles = map[string]bool{
	"withArticlePlainText":        true,
	"withArticleRichContentState": true,
	"withAuxiliaryUserLabels":     false,
	"withDisallowedReplyControls": false,
	"withGrokAnalyze":             false,
	"withPayments":                false,
}

// tweetDetailVariables builds the variables payload for a conversation fetch.
func tweetDetailVariables(tweetID string) map[string]any {
	return map[string]any{
		"focalTweetId":                           tweetID,
		"with_rux_injections":                    false,
		"rankingMode":                            "Relevance",
		"includePromotedContent":                 true,
		"withCommunity":                          true,
		"withQuickPromoteEligibilityTweetFields": true,
		"withBirdwatchNotes":                     true,
		"withVoice":                              true,
	}
}
