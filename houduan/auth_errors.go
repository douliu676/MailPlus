package main

// A quick-mail access key can fail independently of a valid backend login.
// The frontend must only invalidate the session that authenticated a rejected request.
const (
	authErrorHeader       = "X-MailPlus-Auth-Error"
	authErrorSession      = "session"
	authErrorQuickMailKey = "quick-mail-key"
)
