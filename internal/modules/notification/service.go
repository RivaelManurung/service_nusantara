package notification

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/platform/push"
)

// Deps is what the service needs.
//
// Devices and Push are optional and travel together: a deployment with no FCM
// credentials still serves the inbox, and Send reports that push was off
// rather than failing. Following internal/modules/user, they are named fields
// instead of positional arguments so adding one later does not touch every
// call site.
type Deps struct {
	Repo    Repository
	Logger  *slog.Logger
	Devices DeviceRegistry
	Push    push.Sender
}

// Service holds the business rules.
type Service struct {
	repo    Repository
	log     *slog.Logger
	devices DeviceRegistry
	push    push.Sender
}

func NewService(deps Deps) *Service {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Push == nil {
		// A nil sender would panic on the first broadcast; the disabled one
		// refuses in a way the operator can read.
		deps.Push = push.Disabled{}
	}

	return &Service{
		repo:    deps.Repo,
		log:     deps.Logger,
		devices: deps.Devices,
		push:    deps.Push,
	}
}

// List returns one page of the caller's inbox.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Notification, int64, error) {
	if query.UserID == uuid.Nil {
		// A zero owner would match nothing, but silently returning an empty
		// page would hide a wiring mistake in the handler.
		return nil, 0, httpx.Unauthorized("authentication required")
	}

	channel, err := normalizeChannel(query.Channel)
	if err != nil {
		return nil, 0, err
	}
	query.Channel = channel

	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load notifications").WithCause(err)
	}
	return items, total, nil
}

// UnreadCount returns the badge totals for the caller's two inbox tabs.
func (s *Service) UnreadCount(ctx context.Context, userID uuid.UUID) (UnreadCount, error) {
	if userID == uuid.Nil {
		return UnreadCount{}, httpx.Unauthorized("authentication required")
	}

	counts, err := s.repo.UnreadByChannel(ctx, userID)
	if err != nil {
		return UnreadCount{}, httpx.Internal("failed to count unread notifications").WithCause(err)
	}

	transaksi := counts[ChannelTransaksi]
	promo := counts[ChannelPromo]

	return UnreadCount{
		Transaksi: transaksi,
		Promo:     promo,
		Total:     transaksi + promo,
	}, nil
}

// MarkRead acknowledges one message. A message owned by somebody else is
// reported as not found, exactly like one that does not exist: the endpoint
// must not become a way to discover other people's notification ids.
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	if userID == uuid.Nil {
		return httpx.Unauthorized("authentication required")
	}

	if _, err := s.repo.FindByIDForUser(ctx, id, userID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return httpx.NotFound("notification not found")
		}
		return httpx.Internal("failed to load notification").WithCause(err)
	}

	if err := s.repo.MarkRead(ctx, id, userID); err != nil {
		return httpx.Internal("failed to mark notification as read").WithCause(err)
	}
	return nil
}

// MarkAllRead acknowledges every unread message, optionally within one tab, and
// reports how many were updated.
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID, channel string) (int64, error) {
	if userID == uuid.Nil {
		return 0, httpx.Unauthorized("authentication required")
	}

	normalized, err := normalizeChannel(channel)
	if err != nil {
		return 0, err
	}

	updated, err := s.repo.MarkAllRead(ctx, userID, normalized)
	if err != nil {
		return 0, httpx.Internal("failed to mark notifications as read").WithCause(err)
	}
	return updated, nil
}

// --- broadcast ---------------------------------------------------------

// Limits on one broadcast.
const (
	// maxRecipients bounds a single send. A promo to fifty thousand accounts
	// is a background job, not a request an admin waits on with a spinner;
	// refusing is honest, whereas a timeout half way through leaves nobody
	// able to say who was reached.
	maxRecipients = 20000

	maxTitleLength = 255
	maxBodyLength  = 1000
)

// validTypes is the tone the client renders an icon and colour from.
var validTypes = map[string]struct{}{
	"INFO": {}, "SUCCESS": {}, "WARNING": {}, "ERROR": {}, "PROMO": {},
}

// validTargets are the deep-link destinations the mobile app understands. An
// unknown one would render a notification that does nothing when tapped.
var validTargets = map[string]struct{}{
	"": {}, "ORDER": {}, "VOUCHER": {}, "POINT": {},
}

// Send writes one notification per recipient and, when asked, wakes their
// devices.
//
// The inbox is written first and the push is best effort. That order is the
// whole design: a customer who was offline still finds the promo in the app,
// and a Firebase outage costs the tray notification rather than the message.
func (s *Service) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	request, err := normalizeSend(request)
	if err != nil {
		return SendResult{}, err
	}

	recipients, err := s.repo.Recipients(ctx, request.Audience)
	if err != nil {
		return SendResult{}, httpx.Internal("failed to resolve the audience").WithCause(err)
	}

	if len(recipients) == 0 {
		// Reported rather than answered with "sent to 0 people": an operator
		// who mistyped a segment must not be told the promo went out.
		return SendResult{}, httpx.BadRequest("no customer matches this audience")
	}
	if len(recipients) > maxRecipients {
		return SendResult{}, httpx.BadRequest("this audience is too large to send in one go")
	}

	messages := make([]NewNotification, 0, len(recipients))
	for _, userID := range recipients {
		messages = append(messages, NewNotification{
			UserID:      userID,
			Channel:     request.Channel,
			Title:       request.Title,
			Body:        request.Body,
			Type:        request.Type,
			TargetType:  request.TargetType,
			TargetRoute: request.TargetRoute,
			ReferenceID: request.ReferenceID,
		})
	}

	saved, err := s.repo.CreateMany(ctx, messages)
	if err != nil {
		return SendResult{}, httpx.Internal("failed to save notifications").WithCause(err)
	}

	result := SendResult{
		Recipients:  len(recipients),
		Saved:       saved,
		PushEnabled: s.push.Enabled(),
	}

	if request.Push {
		s.deliver(ctx, request, recipients, &result)
	}

	s.log.Info("notification broadcast",
		slog.String("channel", request.Channel),
		slog.Int("recipients", result.Recipients),
		slog.Int64("saved", result.Saved),
		slog.Int("push_sent", result.PushSent),
		slog.Int("push_failed", result.PushFailed))

	// Recorded last and outside any transaction: the messages are the product,
	// this is bookkeeping. A failure to file the history entry must not undo a
	// send that already reached four hundred inboxes -- so it is logged and the
	// send is still reported as done, because it was.
	if _, err := s.repo.RecordBroadcast(ctx, Broadcast{
		Title:          request.Title,
		Body:           request.Body,
		Channel:        request.Channel,
		Type:           request.Type,
		TargetType:     request.TargetType,
		TargetRoute:    request.TargetRoute,
		AudienceMode:   request.Audience.Mode,
		RecipientCount: result.Recipients,
		SavedCount:     result.Saved,
		PushRequested:  request.Push,
		PushEnabled:    result.PushEnabled,
		PushSent:       result.PushSent,
		PushFailed:     result.PushFailed,
		PushError:      result.PushError,
		ActorID:        request.ActorID,
	}); err != nil {
		s.log.Error("broadcast sent but not recorded in the history",
			slog.String("title", request.Title),
			slog.String("error", err.Error()))
	}

	return result, nil
}

// Broadcasts returns the send history for the back office.
//
// This is what the notifications screen lists. It reads notification_broadcasts
// rather than the notifications table: the latter holds one row per recipient,
// so a promo to four hundred customers would appear four hundred times.
func (s *Service) Broadcasts(ctx context.Context, page, perPage int) ([]Broadcast, int64, error) {
	rows, total, err := s.repo.ListBroadcasts(ctx, page, perPage)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load the notification history").WithCause(err)
	}
	return rows, total, nil
}

// SearchCustomers lists candidate recipients for the back-office picker.
func (s *Service) SearchCustomers(ctx context.Context, query CustomerQuery) ([]Customer, int64, error) {
	query.Search = strings.TrimSpace(query.Search)

	items, total, err := s.repo.SearchCustomers(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load customers").WithCause(err)
	}
	return items, total, nil
}

// deliver wakes the recipients' devices, filling in the push half of result.
//
// Nothing here returns an error: the inbox rows are already committed, and
// failing the request now would tell the operator the promo was not sent when
// it was. Problems are reported inside the result instead.
func (s *Service) deliver(ctx context.Context, request SendRequest, recipients []uuid.UUID, result *SendResult) {
	if s.devices == nil || !s.push.Enabled() {
		return
	}

	devices, err := s.devices.TokensFor(ctx, recipients)
	if err != nil {
		s.log.Error("failed to load device tokens", slog.String("error", err.Error()))
		result.PushError = "failed to load device registrations"
		return
	}

	result.Devices = len(devices)
	if len(devices) == 0 {
		return
	}

	data := map[string]string{
		"channel":      request.Channel,
		"type":         request.Type,
		"target_type":  request.TargetType,
		"target_route": request.TargetRoute,
	}
	if request.ReferenceID != nil {
		data["reference_id"] = request.ReferenceID.String()
	}

	messages := make([]push.Message, 0, len(devices))
	for _, device := range devices {
		messages = append(messages, push.Message{
			Token: device.Token,
			Title: request.Title,
			Body:  request.Body,
			Data:  data,
		})
	}

	report, err := s.push.Send(ctx, messages)
	result.PushSent = report.Success
	result.PushFailed = report.Failure

	if err != nil {
		s.log.Error("push delivery failed", slog.String("error", err.Error()))
		result.PushError = "push delivery failed, the notification is still in the inbox"
	}

	// Stale registrations are dropped even when the batch reported an error:
	// they are what FCM told us about this device specifically, and keeping
	// them means paying for them on every future broadcast.
	if len(report.InvalidTokens) > 0 {
		if err := s.devices.DeleteTokens(ctx, report.InvalidTokens); err != nil {
			s.log.Warn("failed to prune stale device tokens",
				slog.Int("count", len(report.InvalidTokens)),
				slog.String("error", err.Error()))
		}
	}
}

// normalizeSend trims, defaults and validates one broadcast.
func normalizeSend(request SendRequest) (SendRequest, error) {
	channel, err := normalizeChannel(request.Channel)
	if err != nil {
		return SendRequest{}, err
	}
	if channel == "" {
		// A broadcast from the back office is a promo unless it says
		// otherwise; the transactional tab is written by the order flow.
		channel = ChannelPromo
	}
	request.Channel = channel

	request.Title = strings.TrimSpace(request.Title)
	if request.Title == "" {
		return SendRequest{}, httpx.BadRequest("title is required")
	}
	if len([]rune(request.Title)) > maxTitleLength {
		return SendRequest{}, httpx.BadRequest("title is too long")
	}

	request.Body = strings.TrimSpace(request.Body)
	if request.Body == "" {
		return SendRequest{}, httpx.BadRequest("body is required")
	}
	if len([]rune(request.Body)) > maxBodyLength {
		return SendRequest{}, httpx.BadRequest("body is too long")
	}

	request.Type = strings.ToUpper(strings.TrimSpace(request.Type))
	if request.Type == "" {
		request.Type = "PROMO"
		if request.Channel == ChannelTransaksi {
			request.Type = "INFO"
		}
	}
	if _, ok := validTypes[request.Type]; !ok {
		return SendRequest{}, httpx.BadRequest("invalid type, allowed: INFO, SUCCESS, WARNING, ERROR, PROMO")
	}

	request.TargetType = strings.ToUpper(strings.TrimSpace(request.TargetType))
	if _, ok := validTargets[request.TargetType]; !ok {
		return SendRequest{}, httpx.BadRequest("invalid target type, allowed: ORDER, VOUCHER, POINT")
	}
	request.TargetRoute = strings.TrimSpace(request.TargetRoute)

	audience, err := normalizeAudience(request.Audience)
	if err != nil {
		return SendRequest{}, err
	}
	request.Audience = audience

	return request, nil
}

// normalizeAudience validates the recipient selection.
func normalizeAudience(audience Audience) (Audience, error) {
	audience.Mode = strings.ToUpper(strings.TrimSpace(audience.Mode))
	if audience.Mode == "" {
		// There is no safe default here. "ALL" would turn a forgotten field
		// into a broadcast to every customer.
		return Audience{}, httpx.BadRequest("audience mode is required, allowed: ALL, USERS, SEGMENT")
	}
	if !IsValidAudienceMode(audience.Mode) {
		return Audience{}, httpx.BadRequest("invalid audience mode, allowed: ALL, USERS, SEGMENT")
	}

	switch audience.Mode {
	case AudienceUsers:
		unique := make([]uuid.UUID, 0, len(audience.UserIDs))
		seen := make(map[uuid.UUID]struct{}, len(audience.UserIDs))
		for _, id := range audience.UserIDs {
			if id == uuid.Nil {
				return Audience{}, httpx.BadRequest("user_ids contains an empty id")
			}
			if _, duplicate := seen[id]; duplicate {
				// The same customer picked twice would otherwise be notified
				// twice for one promo.
				continue
			}
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
		if len(unique) == 0 {
			return Audience{}, httpx.BadRequest("user_ids is required when audience mode is USERS")
		}
		audience.UserIDs = unique
		audience.Segment = Segment{}

	case AudienceSegment:
		audience.Segment.RoleName = strings.TrimSpace(audience.Segment.RoleName)
		if audience.Segment.IsEmpty() {
			return Audience{}, httpx.BadRequest("segment requires at least one filter")
		}
		if from, to := audience.Segment.RegisteredFrom, audience.Segment.RegisteredTo; from != nil && to != nil && to.Before(*from) {
			return Audience{}, httpx.BadRequest("registered_to must not be earlier than registered_from")
		}
		audience.UserIDs = nil

	case AudienceAll:
		audience.UserIDs = nil
		audience.Segment = Segment{}
	}

	return audience, nil
}

// normalizeChannel upper-cases the filter and rejects anything that is not a
// known tab, so an arbitrary string never reaches the WHERE clause.
func normalizeChannel(channel string) (string, error) {
	channel = strings.ToUpper(strings.TrimSpace(channel))
	if channel == "" {
		return "", nil
	}
	if !IsValidChannel(channel) {
		return "", httpx.BadRequest("invalid channel, allowed: TRANSAKSI, PROMO")
	}
	return channel, nil
}
