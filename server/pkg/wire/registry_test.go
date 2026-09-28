package wire

import "testing"

// bodyRegistry maps a body type's NAME to a zero value of that type.
//
// It exists because Go cannot instantiate a type from a string: the mapping tests
// discover the declared *Body types by parsing the package source, and this is
// how they turn a discovered name back into a value they can marshal.
//
// Hand-written on purpose, and guarded: a new *Body type missing from here fails
// TestEveryDeclaredBodyTypeIsCoveredByThisTest with the name to add. That keeps
// the list honest without letting it silently cover nothing.
var bodyRegistry = map[string]any{
	"AccountDeleteBody":    AccountDeleteBody{},
	"AccountDeletedBody":   AccountDeletedBody{},
	"AuthBody":             AuthBody{},
	"AuthOKBody":           AuthOKBody{},
	"BlockBody":            BlockBody{},
	"BillingCancelBody":    BillingCancelBody{},
	"BillingCheckoutBody":  BillingCheckoutBody{},
	"BillingOffersBody":    BillingOffersBody{},
	"BillingPaymentBody":   BillingPaymentBody{},
	"BillingPlansBody":     BillingPlansBody{},
	"BillingStatusBody":    BillingStatusBody{},
	"SubscriptionBody":     SubscriptionBody{},
	"CallActionBody":       CallActionBody{},
	"CallInviteBody":       CallInviteBody{},
	"CallSignalBody":       CallSignalBody{},
	"CallStateBody":        CallStateBody{},
	"ChatCreateBody":       ChatCreateBody{},
	"ChatExportBody":       ChatExportBody{},
	"ChatExportResultBody": ChatExportResultBody{},
	"ChatInfoBody":         ChatInfoBody{},
	"ChatFlagsBody":        ChatFlagsBody{},
	"ChatFlagsSetBody":     ChatFlagsSetBody{},
	"ChatListBody":         ChatListBody{},
	"ChatsBody":            ChatsBody{},
	"ContactAddBody":       ContactAddBody{},
	"ContactListBody":      ContactListBody{},
	"ContactRemoveBody":    ContactRemoveBody{},
	"ContactSyncBody":      ContactSyncBody{},
	"DeleteBody":           DeleteBody{},
	"DraftBody":            DraftBody{},
	"DraftSyncBody":        DraftSyncBody{},
	"DraftsBody":           DraftsBody{},
	"EditBody":             EditBody{},
	"ErrorBody":            ErrorBody{},
	"FanoutShardBody":      FanoutShardBody{},
	"ForwardBody":          ForwardBody{},
	"HelloBody":            HelloBody{},
	"HistoryPageBody":      HistoryPageBody{},
	"HistoryBody":          HistoryBody{},
	"HistoryOKBody":        HistoryOKBody{},
	"InviteCreateBody":     InviteCreateBody{},
	"InviteListBody":       InviteListBody{},
	"InviteRevokeBody":     InviteRevokeBody{},
	"InvitesBody":          InvitesBody{},
	"JoinBody":             JoinBody{},
	"KeyBundleBody":        KeyBundleBody{},
	"SecretAckBody":        SecretAckBody{},
	"SecretAckedBody":      SecretAckedBody{},
	"SecretSyncBody":       SecretSyncBody{},
	"SecretSyncedBody":     SecretSyncedBody{},
	"KeyBundlesBody":       KeyBundlesBody{},
	"KeyFetchBody":         KeyFetchBody{},
	"KeyPublishBody":       KeyPublishBody{},
	"KeyStateBody":         KeyStateBody{},
	"MediaFetchBody":       MediaFetchBody{},
	"MediaInitBody":        MediaInitBody{},
	"MediaTicketBody":      MediaTicketBody{},
	"MediaURLBody":         MediaURLBody{},
	"NewMessageBody":       NewMessageBody{},
	"PinBody":              PinBody{},
	"PinnedBody":           PinnedBody{},
	"PollCloseBody":        PollCloseBody{},
	"PollCreateBody":       PollCreateBody{},
	"PollStateBody":        PollStateBody{},
	"PollVoteBody":         PollVoteBody{},
	"PresenceBody":         PresenceBody{},
	"PasswordChangeBody":   PasswordChangeBody{},
	"PasswordChangedBody":  PasswordChangedBody{},
	"TOTPConfirmBody":      TOTPConfirmBody{},
	"TOTPDisableBody":      TOTPDisableBody{},
	"TOTPSetupBody":        TOTPSetupBody{},
	"TOTPSetupInfoBody":    TOTPSetupInfoBody{},
	"TOTPStateBody":        TOTPStateBody{},
	"PrivacyBody":          PrivacyBody{},
	"PrivacyGetBody":       PrivacyGetBody{},
	"PrivacySetBody":       PrivacySetBody{},
	"ProfileBody":          ProfileBody{},
	"ProfileGetBody":       ProfileGetBody{},
	"ProfileSetBody":       ProfileSetBody{},
	"PushTokenBody":        PushTokenBody{},
	"ReactBody":            ReactBody{},
	"ReactUpdateBody":      ReactUpdateBody{},
	"ReadBody":             ReadBody{},
	"ReadUpdateBody":       ReadUpdateBody{},
	"ResumeBody":           ResumeBody{},
	"ResumeOKBody":         ResumeOKBody{},
	"ScheduleBody":         ScheduleBody{},
	"ScheduleCancelBody":   ScheduleCancelBody{},
	"ScheduleListBody":     ScheduleListBody{},
	"ScheduledBody":        ScheduledBody{},
	"SessionListBody":      SessionListBody{},
	"SessionRevokeBody":    SessionRevokeBody{},
	"SessionRevokedBody":   SessionRevokedBody{},
	"SessionsBody":         SessionsBody{},
	"SearchBody":           SearchBody{},
	"SearchResultsBody":    SearchResultsBody{},
	"SecretMsgBody":        SecretMsgBody{},
	"SendAckBody":          SendAckBody{},
	"SendBody":             SendBody{},
	"SetRoleBody":          SetRoleBody{},
	"SetUsernameBody":      SetUsernameBody{},
	"ThreadBody":           ThreadBody{},
	"ThreadOKBody":         ThreadOKBody{},
	"TypingBody":           TypingBody{},
	"WelcomeBody":          WelcomeBody{},
}

// TestErrorCodesAreUnique catches a collision Go will not.
//
// Two constants with the same value compile silently, so `ErrTwoFactorRequired =
// 2003` sat on top of `ErrDeviceUnknown = 2003` without a warning — and the effect
// would have been a client logging itself out when it was asked for a two-factor
// code, because 2xxx is the range that means "your session is void".
//
// The list is hand-written and guarded by the count assertion below, which is the
// same bargain bodyRegistry makes: a new code missing from here fails a test that
// names it, rather than quietly covering nothing.
func TestErrorCodesAreUnique(t *testing.T) {
	codes := map[string]ErrorCode{
		"ErrNone":              ErrNone,
		"ErrProtocol":          ErrProtocol,
		"ErrBadFrame":          ErrBadFrame,
		"ErrUnsupported":       ErrUnsupported,
		"ErrPayloadTooBig":     ErrPayloadTooBig,
		"ErrResumeExpired":     ErrResumeExpired,
		"ErrUnauthenticated":   ErrUnauthenticated,
		"ErrBadToken":          ErrBadToken,
		"ErrSessionRevoked":    ErrSessionRevoked,
		"ErrDeviceUnknown":     ErrDeviceUnknown,
		"ErrTwoFactorRequired": ErrTwoFactorRequired,
		"ErrResumeReplayed":    ErrResumeReplayed,
		"ErrForbidden":         ErrForbidden,
		"ErrNotFound":          ErrNotFound,
		"ErrConflict":          ErrConflict,
		"ErrBadArg":            ErrBadArg,
		"ErrTwoFactorInvalid":  ErrTwoFactorInvalid,
		"ErrPremiumRequired":   ErrPremiumRequired,
		"ErrRateLimited":       ErrRateLimited,
		"ErrFlood":             ErrFlood,
		"ErrInternal":          ErrInternal,
		"ErrUnavailable":       ErrUnavailable,
	}
	seen := map[ErrorCode]string{}
	for name, code := range codes {
		if other, dup := seen[code]; dup {
			t.Errorf("%s and %s share code %d", name, other, code)
		}
		seen[code] = name
	}

	// The RANGE is part of the contract, not decoration: a client maps 2xxx to
	// "re-authenticate" and 3xxx to "that request failed". A code in the wrong band
	// makes a client do the wrong thing without either side being wrong about the
	// code itself.
	//
	// The two that matter most: a bad two-factor code arrives on an AUTHENTICATED
	// connection (confirming enrolment, disabling the factor), and so does
	// premium-required — putting either in 2xxx signs the user out.
	for name, code := range map[string]ErrorCode{
		"ErrTwoFactorInvalid": ErrTwoFactorInvalid,
		"ErrPremiumRequired":  ErrPremiumRequired,
	} {
		if code < 3000 || code >= 4000 {
			t.Errorf("%s = %d is outside the business range; an auth-class code would "+
				"make a client discard a live session", name, code)
		}
	}
	for name, code := range map[string]ErrorCode{
		"ErrTwoFactorRequired": ErrTwoFactorRequired,
		"ErrResumeReplayed":    ErrResumeReplayed,
	} {
		if code < 2000 || code >= 3000 {
			t.Errorf("%s = %d is outside the auth range; it means the session is gone", name, code)
		}
	}
}
