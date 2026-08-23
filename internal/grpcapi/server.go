// Package grpcapi implements the AudioServerService gRPC API.
package grpcapi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
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

type Server struct {
	audioserverv1.UnimplementedAudioServerServiceServer

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

func (s *Server) RegisterStation(ctx context.Context, req *audioserverv1.RegisterStationRequest) (*audioserverv1.RegisterStationResponse, error) {
	if req.GetSlug() == "" {
		return nil, status.Error(codes.InvalidArgument, "slug is required")
	}
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, reRegistered := s.registry.Register(req.GetSlug(), req.GetName(), req.GetDescription(), req.GetLogoUrl(), req.GetMetadata(), req.GetLowQueueThreshold(), func(newStation *playback.Station) {
		if s.starter != nil {
			s.starter.StartStation(newStation)
		}
	})

	s.log.Info("station registered", "slug", st.Slug, "name", st.Name(), "re_registered", reRegistered)

	return &audioserverv1.RegisterStationResponse{
		Slug:         st.Slug,
		StreamUrl:    fmt.Sprintf("%s/stream/%s", s.publicBaseURL, st.Slug),
		ReRegistered: reRegistered,
	}, nil
}

func (s *Server) UnregisterStation(ctx context.Context, req *audioserverv1.UnregisterStationRequest) (*audioserverv1.UnregisterStationResponse, error) {
	if req.GetSlug() == "" {
		return nil, status.Error(codes.InvalidArgument, "slug is required")
	}
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Unregister(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}
	st.Stop()

	s.log.Info("station unregistered", "slug", req.GetSlug())

	return &audioserverv1.UnregisterStationResponse{}, nil
}

// ListStations returns every registered station the caller's token
// authorizes (per Claims.HasSlug), each with its current listener count.
// Unlike GetStatus it takes no slug -- auth.RequireSlug doesn't apply to a
// single station here, so unauthorized stations are silently filtered out
// of the result rather than the call being rejected outright.
func (s *Server) ListStations(ctx context.Context, req *audioserverv1.ListStationsRequest) (*audioserverv1.ListStationsResponse, error) {
	claims, ok := auth.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no claims in context")
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

	return resp, nil
}

func (s *Server) QueueTrack(ctx context.Context, req *audioserverv1.QueueTrackRequest) (*audioserverv1.QueueTrackResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}
	if req.GetSource() == nil {
		return nil, status.Error(codes.InvalidArgument, "source is required")
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	transition := req.GetTransition()
	if transition == audioserverv1.Transition_TRANSITION_CROSSFADE {
		s.log.Warn("crossfade transition requested but not implemented this phase; coercing to hard cut", "slug", req.GetSlug())
		transition = audioserverv1.Transition_TRANSITION_HARD_CUT
	}

	item := playback.NewQueuedItem(uuid.NewString(), req.GetSource(), req.GetMode(), transition)
	if s.prefetcher != nil {
		s.prefetcher.Prefetch(item)
	} else {
		item.MarkReady("", 0, fmt.Errorf("no prefetcher configured"))
	}

	var position int
	switch req.GetMode() {
	case audioserverv1.QueueMode_QUEUE_MODE_PLAY_NEXT, audioserverv1.QueueMode_QUEUE_MODE_PLAY_NOW_INTERRUPT:
		st.Queue.PushFront(item)
		position = 0
		if req.GetMode() == audioserverv1.QueueMode_QUEUE_MODE_PLAY_NOW_INTERRUPT {
			st.Interrupt()
		}
	default:
		position = st.Queue.Append(item)
	}

	st.QueueChanged()

	return &audioserverv1.QueueTrackResponse{
		QueueId:       item.ID,
		QueuePosition: int32(position),
		Status:        "queued",
	}, nil
}

func (s *Server) RemoveFromQueue(ctx context.Context, req *audioserverv1.RemoveFromQueueRequest) (*audioserverv1.RemoveFromQueueResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	removed := st.Queue.Remove(req.GetQueueId())
	if removed {
		st.QueueChanged()
	}

	return &audioserverv1.RemoveFromQueueResponse{Removed: removed}, nil
}

func (s *Server) ClearQueue(ctx context.Context, req *audioserverv1.ClearQueueRequest) (*audioserverv1.ClearQueueResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	removedCount := st.Queue.Clear()
	if removedCount > 0 {
		st.QueueChanged()
	}

	stoppedCurrent := false
	if req.GetStopCurrent() && st.Current() != nil {
		st.Interrupt()
		stoppedCurrent = true
	}

	return &audioserverv1.ClearQueueResponse{RemovedCount: int32(removedCount), StoppedCurrent: stoppedCurrent}, nil
}

func (s *Server) Skip(ctx context.Context, req *audioserverv1.SkipRequest) (*audioserverv1.SkipResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	if st.Current() == nil {
		return &audioserverv1.SkipResponse{Skipped: false}, nil
	}
	st.Interrupt()
	return &audioserverv1.SkipResponse{Skipped: true}, nil
}

func (s *Server) SkipTo(ctx context.Context, req *audioserverv1.SkipToRequest) (*audioserverv1.SkipToResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	removedCount, found := st.Queue.SkipTo(req.GetQueueId())
	if !found {
		return nil, status.Errorf(codes.NotFound, "queue item %q not found", req.GetQueueId())
	}
	if removedCount > 0 {
		st.QueueChanged()
	}

	interruptedCurrent := st.Current() != nil
	st.Interrupt()

	return &audioserverv1.SkipToResponse{RemovedCount: int32(removedCount), InterruptedCurrent: interruptedCurrent}, nil
}

func (s *Server) Pause(ctx context.Context, req *audioserverv1.PauseRequest) (*audioserverv1.PauseResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	return &audioserverv1.PauseResponse{Paused: st.Pause()}, nil
}

func (s *Server) Resume(ctx context.Context, req *audioserverv1.ResumeRequest) (*audioserverv1.ResumeResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	return &audioserverv1.ResumeResponse{Resumed: st.Resume()}, nil
}

func (s *Server) Seek(ctx context.Context, req *audioserverv1.SeekRequest) (*audioserverv1.SeekResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	seeked, position := st.SeekPosition(req.GetPositionSeconds())
	return &audioserverv1.SeekResponse{Seeked: seeked, PositionSeconds: position}, nil
}

func (s *Server) SeekBy(ctx context.Context, req *audioserverv1.SeekByRequest) (*audioserverv1.SeekByResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if err := auth.RequireWrite(ctx); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	seeked, position := st.SeekBy(req.GetDeltaSeconds())
	return &audioserverv1.SeekByResponse{Seeked: seeked, PositionSeconds: position}, nil
}

func (s *Server) GetStatus(ctx context.Context, req *audioserverv1.GetStatusRequest) (*audioserverv1.GetStatusResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return &audioserverv1.GetStatusResponse{Slug: req.GetSlug(), IsRegistered: false}, nil
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

	return resp, nil
}

func (s *Server) SubscribeEvents(req *audioserverv1.SubscribeEventsRequest, stream audioserverv1.AudioServerService_SubscribeEventsServer) error {
	ctx := stream.Context()
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
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
func (s *Server) GetServerInfo(ctx context.Context, req *audioserverv1.GetServerInfoRequest) (*audioserverv1.GetServerInfoResponse, error) {
	return &audioserverv1.GetServerInfoResponse{Version: version.Version}, nil
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
