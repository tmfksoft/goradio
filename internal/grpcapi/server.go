// Package grpcapi implements the AudioServerService RPC API.
//
// Handlers are written against connect-go rather than grpc-go, which
// means one handler serves three protocols on the same endpoint: gRPC
// (what the built-in station controller dials with), gRPC-Web, and
// Connect's own protocol -- the last of which is plain HTTP POST with a
// JSON body, so a client with nothing but an HTTP/1.1 stack and a JSON
// parser can drive the server without a gRPC or protobuf library.
package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
	"github.com/tmfksoft/goradio/gen/go/audioserver/v1/audioserverv1connect"
	"github.com/tmfksoft/goradio/internal/auth"
	"github.com/tmfksoft/goradio/internal/playback"
	"github.com/tmfksoft/goradio/internal/registry"
	"github.com/tmfksoft/goradio/internal/version"
)

// Prefetcher dispatches a queued item's download/transcode job. Handed in
// as an interface so grpcapi doesn't depend on the transcode package's
// concrete config/cache types.
type Prefetcher interface {
	Prefetch(item *playback.QueuedItem)
}

// StationStarter is called once, the first time a station is registered,
// so the caller (cmd/radio's serve wiring) can start its player goroutine.
type StationStarter interface {
	StartStation(st *playback.Station)
}

// Compile-time check that Server satisfies the generated handler
// interface -- connect-go's constructor takes it structurally, so without
// this a signature that drifts out of sync with a regenerated stub fails
// at the call site in cmd/radio rather than here.
var _ audioserverv1connect.AudioServerServiceHandler = (*Server)(nil)

type Server struct {
	log           *slog.Logger
	registry      *registry.Registry
	prefetcher    Prefetcher
	starter       StationStarter
	publicBaseURL string
}

func NewServer(log *slog.Logger, reg *registry.Registry, prefetcher Prefetcher, starter StationStarter, publicBaseURL string) *Server {
	return &Server{
		log:           log,
		registry:      reg,
		prefetcher:    prefetcher,
		starter:       starter,
		publicBaseURL: publicBaseURL,
	}
}

// notRegistered is the error every handler returns for a slug that isn't
// in the registry, so the code/message stay identical across all of them.
func notRegistered(slug string) error {
	return connect.NewError(connect.CodeNotFound, fmt.Errorf("station %q is not registered", slug))
}

func (s *Server) RegisterStation(ctx context.Context, req *connect.Request[audioserverv1.RegisterStationRequest]) (*connect.Response[audioserverv1.RegisterStationResponse], error) {
	if req.Msg.GetSlug() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("slug is required"))
	}
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, reRegistered := s.registry.Register(req.Msg.GetSlug(), req.Msg.GetName(), req.Msg.GetDescription(), req.Msg.GetLogoUrl(), req.Msg.GetMetadata(), req.Msg.GetLowQueueThreshold(), func(newStation *playback.Station) {
		if s.starter != nil {
			s.starter.StartStation(newStation)
		}
	})

	s.log.Info("station registered", "slug", st.Slug, "name", st.Name(), "re_registered", reRegistered)

	return connect.NewResponse(&audioserverv1.RegisterStationResponse{
		Slug:         st.Slug,
		StreamUrl:    fmt.Sprintf("%s/stream/%s", s.publicBaseURL, st.Slug),
		ReRegistered: reRegistered,
	}), nil
}

func (s *Server) UnregisterStation(ctx context.Context, req *connect.Request[audioserverv1.UnregisterStationRequest]) (*connect.Response[audioserverv1.UnregisterStationResponse], error) {
	if req.Msg.GetSlug() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("slug is required"))
	}
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Unregister(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}
	st.Stop()

	s.log.Info("station unregistered", "slug", req.Msg.GetSlug())

	return connect.NewResponse(&audioserverv1.UnregisterStationResponse{}), nil
}

// ListStations returns every registered station the caller's token
// authorizes (per Claims.HasSlug), each with its current listener count.
// Unlike GetStatus it takes no slug -- auth.RequireSlug doesn't apply to a
// single station here, so unauthorized stations are silently filtered out
// of the result rather than the call being rejected outright.
func (s *Server) ListStations(ctx context.Context, req *connect.Request[audioserverv1.ListStationsRequest]) (*connect.Response[audioserverv1.ListStationsResponse], error) {
	claims, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no claims in context"))
	}

	resp := &audioserverv1.ListStationsResponse{}
	for _, st := range s.registry.List() {
		if !claims.HasSlug(st.Slug) {
			continue
		}
		resp.Stations = append(resp.Stations, &audioserverv1.StationSummary{
			Slug:          st.Slug,
			Name:          st.Name(),
			ListenerCount: int64(st.Broadcaster.ListenerCount()),
			LogoUrl:       st.LogoURL(),
			Metadata:      st.Metadata(),
		})
	}

	return connect.NewResponse(resp), nil
}

func (s *Server) QueueTrack(ctx context.Context, req *connect.Request[audioserverv1.QueueTrackRequest]) (*connect.Response[audioserverv1.QueueTrackResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}
	if req.Msg.GetSource() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("source is required"))
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	transition := req.Msg.GetTransition()
	if transition == audioserverv1.Transition_TRANSITION_CROSSFADE {
		s.log.Warn("crossfade transition requested but not implemented this phase; coercing to hard cut", "slug", req.Msg.GetSlug())
		transition = audioserverv1.Transition_TRANSITION_HARD_CUT
	}

	item := playback.NewQueuedItem(uuid.NewString(), req.Msg.GetSource(), req.Msg.GetMode(), transition)
	if s.prefetcher != nil {
		s.prefetcher.Prefetch(item)
	} else {
		item.MarkReady("", 0, fmt.Errorf("no prefetcher configured"))
	}

	var position int
	switch req.Msg.GetMode() {
	case audioserverv1.QueueMode_QUEUE_MODE_PLAY_NEXT, audioserverv1.QueueMode_QUEUE_MODE_PLAY_NOW_INTERRUPT:
		st.Queue.PushFront(item)
		position = 0
		if req.Msg.GetMode() == audioserverv1.QueueMode_QUEUE_MODE_PLAY_NOW_INTERRUPT {
			st.Interrupt()
		}
	default:
		position = st.Queue.Append(item)
	}

	st.QueueChanged()

	return connect.NewResponse(&audioserverv1.QueueTrackResponse{
		QueueId:       item.ID,
		QueuePosition: int32(position),
		Status:        "queued",
	}), nil
}

func (s *Server) RemoveFromQueue(ctx context.Context, req *connect.Request[audioserverv1.RemoveFromQueueRequest]) (*connect.Response[audioserverv1.RemoveFromQueueResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	removed := st.Queue.Remove(req.Msg.GetQueueId())
	if removed {
		st.QueueChanged()
	}

	return connect.NewResponse(&audioserverv1.RemoveFromQueueResponse{Removed: removed}), nil
}

func (s *Server) ClearQueue(ctx context.Context, req *connect.Request[audioserverv1.ClearQueueRequest]) (*connect.Response[audioserverv1.ClearQueueResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	removedCount := st.Queue.Clear()
	if removedCount > 0 {
		st.QueueChanged()
	}

	stoppedCurrent := false
	if req.Msg.GetStopCurrent() && st.Current() != nil {
		st.Interrupt()
		stoppedCurrent = true
	}

	return connect.NewResponse(&audioserverv1.ClearQueueResponse{RemovedCount: int32(removedCount), StoppedCurrent: stoppedCurrent}), nil
}

func (s *Server) Skip(ctx context.Context, req *connect.Request[audioserverv1.SkipRequest]) (*connect.Response[audioserverv1.SkipResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	if st.Current() == nil {
		return connect.NewResponse(&audioserverv1.SkipResponse{Skipped: false}), nil
	}
	st.Interrupt()
	return connect.NewResponse(&audioserverv1.SkipResponse{Skipped: true}), nil
}

func (s *Server) SkipTo(ctx context.Context, req *connect.Request[audioserverv1.SkipToRequest]) (*connect.Response[audioserverv1.SkipToResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	removedCount, found := st.Queue.SkipTo(req.Msg.GetQueueId())
	if !found {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("queue item %q not found", req.Msg.GetQueueId()))
	}
	if removedCount > 0 {
		st.QueueChanged()
	}

	interruptedCurrent := st.Current() != nil
	st.Interrupt()

	return connect.NewResponse(&audioserverv1.SkipToResponse{RemovedCount: int32(removedCount), InterruptedCurrent: interruptedCurrent}), nil
}

func (s *Server) Pause(ctx context.Context, req *connect.Request[audioserverv1.PauseRequest]) (*connect.Response[audioserverv1.PauseResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	return connect.NewResponse(&audioserverv1.PauseResponse{Paused: st.Pause()}), nil
}

func (s *Server) Resume(ctx context.Context, req *connect.Request[audioserverv1.ResumeRequest]) (*connect.Response[audioserverv1.ResumeResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	return connect.NewResponse(&audioserverv1.ResumeResponse{Resumed: st.Resume()}), nil
}

func (s *Server) Seek(ctx context.Context, req *connect.Request[audioserverv1.SeekRequest]) (*connect.Response[audioserverv1.SeekResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	seeked, position := st.SeekPosition(req.Msg.GetPositionSeconds())
	return connect.NewResponse(&audioserverv1.SeekResponse{Seeked: seeked, PositionSeconds: position}), nil
}

func (s *Server) SeekBy(ctx context.Context, req *connect.Request[audioserverv1.SeekByRequest]) (*connect.Response[audioserverv1.SeekByResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return nil, notRegistered(req.Msg.GetSlug())
	}

	seeked, position := st.SeekBy(req.Msg.GetDeltaSeconds())
	return connect.NewResponse(&audioserverv1.SeekByResponse{Seeked: seeked, PositionSeconds: position}), nil
}

func (s *Server) GetStatus(ctx context.Context, req *connect.Request[audioserverv1.GetStatusRequest]) (*connect.Response[audioserverv1.GetStatusResponse], error) {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return connect.NewResponse(&audioserverv1.GetStatusResponse{Slug: req.Msg.GetSlug(), IsRegistered: false}), nil
	}

	resp := &audioserverv1.GetStatusResponse{
		Slug:          st.Slug,
		Name:          st.Name(),
		IsRegistered:  true,
		IsSilence:     st.IsSilence(),
		IsPaused:      st.IsPaused(),
		ListenerCount: int64(st.Broadcaster.ListenerCount()),
		UptimeSeconds: int64(st.Uptime().Seconds()),
		LogoUrl:       st.LogoURL(),
		Metadata:      st.Metadata(),
	}

	if cur := st.Current(); cur != nil {
		resp.CurrentTrack = queuedItemToStatus(cur)
		resp.CurrentTrackElapsedSeconds = st.CurrentElapsedSeconds()
	}

	for _, item := range st.Queue.Snapshot() {
		resp.Queue = append(resp.Queue, queuedItemToStatus(item))
	}

	for _, h := range st.History() {
		resp.History = append(resp.History, historyEntryToStatus(h))
	}

	return connect.NewResponse(resp), nil
}

func (s *Server) SubscribeEvents(ctx context.Context, req *connect.Request[audioserverv1.SubscribeEventsRequest], stream *connect.ServerStream[audioserverv1.StationEvent]) error {
	if err := auth.RequireSlug(ctx, req.Msg.GetSlug()); err != nil {
		return err
	}

	st, ok := s.registry.Get(req.Msg.GetSlug())
	if !ok {
		return notRegistered(req.Msg.GetSlug())
	}

	_, ch, unsubscribe := st.Events.Subscribe()
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

// GetServerInfo reports the audio server's build version. Not scoped to
// any station -- unlike every other RPC here, it doesn't call
// auth.RequireSlug, since the interceptor (see internal/auth/interceptor.go)
// already rejects any unauthenticated call before this handler runs; no
// further per-slug authorization applies to a fact about the server
// itself, same reasoning as ListStations not needing it either.
func (s *Server) GetServerInfo(ctx context.Context, req *connect.Request[audioserverv1.GetServerInfoRequest]) (*connect.Response[audioserverv1.GetServerInfoResponse], error) {
	return connect.NewResponse(&audioserverv1.GetServerInfoResponse{Version: version.Version}), nil
}

func queuedItemToStatus(item *playback.QueuedItem) *audioserverv1.QueuedItemStatus {
	return &audioserverv1.QueuedItemStatus{
		QueueId:         item.ID,
		Source:          item.Source,
		Mode:            item.Mode,
		DurationSeconds: item.DurationSeconds(),
	}
}

func historyEntryToStatus(h *playback.HistoryEntry) *audioserverv1.HistoryEntryStatus {
	return &audioserverv1.HistoryEntryStatus{
		QueueId:         h.Item.ID,
		Source:          h.Item.Source,
		Mode:            h.Item.Mode,
		DurationSeconds: h.Item.DurationSeconds(),
		Reason:          h.Reason,
		EndedAtUnixMs:   h.EndedAt.UnixMilli(),
	}
}
