package birdtool

import "slices"

var apiCommands = []string{
	"about", "bookmarks", "check", "follow", "followers", "following", "home", "likes",
	"list-timeline", "lists", "mentions", "news", "query-ids", "read", "replies", "reply",
	"search", "thread", "tweet", "unbookmark", "unfollow", "user-tweets", "whoami",
}

var modelCommands = []string{
	"about", "bookmarks", "check", "follow", "followers", "following", "home", "likes",
	"list-timeline", "lists", "mentions", "news", "query-ids", "read", "replies", "reply",
	"scrape", "search", "thread", "tweet", "unbookmark", "unfollow", "user-tweets",
}

func APIAllowed(command string) bool {
	return slices.Contains(apiCommands, command)
}

func ModelCommands() []string {
	return slices.Clone(modelCommands)
}
