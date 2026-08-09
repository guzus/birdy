package xapi

import "testing"

func TestParseUserPreservesCountPresence(t *testing.T) {
	withZero, err := parseUser([]byte(`{"data":{"user":{"result":{"rest_id":"1","legacy":{"screen_name":"zero","name":"Zero","followers_count":0,"friends_count":0,"statuses_count":0}}}}}`), "zero")
	if err != nil {
		t.Fatal(err)
	}
	if withZero.Followers == nil || *withZero.Followers != 0 || withZero.Following == nil || withZero.Tweets == nil {
		t.Fatalf("reported zero counts lost presence: %+v", withZero)
	}

	omitted, err := parseUser([]byte(`{"data":{"user":{"result":{"rest_id":"2","core":{"screen_name":"core","name":"Core"}}}}}`), "core")
	if err != nil {
		t.Fatal(err)
	}
	if omitted.Followers != nil || omitted.Following != nil || omitted.Tweets != nil {
		t.Fatalf("omitted counts became reported zeros: %+v", omitted)
	}
}
