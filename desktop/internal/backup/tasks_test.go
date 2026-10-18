package backup

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

type taskResult struct {
	status string
	result string
	key    *client.KeyTaskResult
	kind   string // "result", "key_share", "rewrap"
}

type fakeTaskAPI struct {
	mu      sync.Mutex
	polls   [][]wire.DeviceTask
	pollErr []error
	nPoll   int
	results map[int64]taskResult
	done    chan struct{} // closed when every polled task has a result
	want    int
}

func (f *fakeTaskAPI) PollTasks(ctx context.Context, wait time.Duration) ([]wire.DeviceTask, error) {
	f.mu.Lock()
	n := f.nPoll
	f.nPoll++
	f.mu.Unlock()
	if n < len(f.pollErr) && f.pollErr[n] != nil {
		return nil, f.pollErr[n]
	}
	if n < len(f.polls) {
		return f.polls[n], nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeTaskAPI) record(id int64, r taskResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[id] = r
	if len(f.results) == f.want && f.done != nil {
		close(f.done)
		f.done = nil
	}
	return nil
}

func (f *fakeTaskAPI) TaskResult(_ context.Context, id int64, status string, result json.RawMessage) error {
	return f.record(id, taskResult{kind: "result", status: status, result: string(result)})
}

func (f *fakeTaskAPI) FinishKeyShare(_ context.Context, id int64, res client.KeyTaskResult) error {
	return f.record(id, taskResult{kind: "key_share", key: &res})
}

func (f *fakeTaskAPI) FinishRewrap(_ context.Context, id int64, res client.KeyTaskResult) error {
	return f.record(id, taskResult{kind: "rewrap", key: &res})
}

type fakeBackups struct {
	mu      sync.Mutex
	started []int64
	retried []wire.TaskRetrySkippedPayload
	busy    bool
}

func (b *fakeBackups) Start(_ context.Context, jobID int64, trigger string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.busy {
		return 0, ErrRunBusy
	}
	if trigger != wire.RunTriggerTask {
		return 0, errors.New("wrong trigger " + trigger)
	}
	b.started = append(b.started, jobID)
	return 40 + jobID, nil
}

func (b *fakeBackups) RetrySkipped(_ context.Context, p wire.TaskRetrySkippedPayload) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retried = append(b.retried, p)
	if p.RunID == 0 {
		return 0, ErrUnknownRun
	}
	return 77, nil
}

type fakeKeys struct{}

func (fakeKeys) HandleTask(_ context.Context, t wire.DeviceTask) (any, error) {
	if t.Kind == wire.TaskKindRewrap {
		return client.KeyTaskResult{RepoID: 7, Error: "stale epoch"}, errors.New("stale epoch")
	}
	return client.KeyTaskResult{RepoID: 7, Epoch: 2, KeyFileOID: "k"}, nil
}

func task(id int64, kind, payload string) wire.DeviceTask {
	return wire.DeviceTask{ID: id, Kind: kind, Payload: json.RawMessage(payload), Status: wire.TaskStatusClaimed}
}

func TestTaskLoopDispatch(t *testing.T) {
	api := &fakeTaskAPI{results: map[int64]taskResult{}, done: make(chan struct{}), want: 8,
		pollErr: []error{&client.APIError{Status: 503, Code: "capacity"}},
		polls: [][]wire.DeviceTask{nil, {
			task(1, wire.TaskKindBackupNow, `{"job_id":5}`),
			task(2, wire.TaskKindRetrySkipped, `{"run_id":9,"issue_ids":[1,2]}`),
			task(3, wire.TaskKindKeyShare, `{"repo_id":7,"key_file_oid":"k"}`),
			task(4, wire.TaskKindRewrap, `{"repo_id":7,"epoch":3}`),
		}, {
			task(5, wire.TaskKindVerify, `{"repo_id":7,"depth":"metadata"}`),
			task(6, wire.TaskKindBackupNow, `{"job":5}`),
			task(7, wire.TaskKindRestore, `{"restore_id":4}`),
			task(8, wire.TaskKindRetrySkipped, `{"run_id":0}`),
		}}}
	bk := &fakeBackups{}
	loop := &TaskLoop{API: api, Backups: bk, Keys: fakeKeys{}, MinBackoff: time.Millisecond,
		Handlers: map[string]TaskHandler{wire.TaskKindRestore: func(_ context.Context, t wire.DeviceTask) (any, error) {
			return map[string]int64{"restore_id": 4}, nil
		}}}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- loop.Run(ctx) }()
	select {
	case <-api.done:
	case <-time.After(5 * time.Second):
		t.Fatal("tasks not finished")
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}

	r := api.results
	check := func(id int64, kind, status, result string) {
		t.Helper()
		if r[id].kind != kind || r[id].status != status || (result != "" && r[id].result != result) {
			t.Fatalf("task %d: %+v", id, r[id])
		}
	}
	check(1, "result", wire.TaskStatusDone, `{"job_id":5,"run_id":45}`)
	check(2, "result", wire.TaskStatusDone, `{"run_id":77}`)
	if k := r[3].key; r[3].kind != "key_share" || k == nil || k.Error != "" || k.Epoch != 2 {
		t.Fatalf("key_share %+v", r[3])
	}
	if k := r[4].key; r[4].kind != "rewrap" || k == nil || k.Error != "stale epoch" {
		t.Fatalf("rewrap %+v", r[4])
	}
	check(5, "result", wire.TaskStatusFailed, `{"error":"unsupported task kind \"verify\""}`)
	check(6, "result", wire.TaskStatusFailed, "")
	check(7, "result", wire.TaskStatusDone, `{"restore_id":4}`)
	check(8, "result", wire.TaskStatusFailed, `{"error":"backup: unknown run"}`)
	if len(bk.retried) != 2 || len(bk.retried[0].IssueIDs) != 2 || len(bk.started) != 1 {
		t.Fatalf("backups %+v", bk)
	}
}

func TestTaskLoopBusyAndMissing(t *testing.T) {
	api := &fakeTaskAPI{results: map[int64]taskResult{}}
	loop := &TaskLoop{API: api, Backups: &fakeBackups{busy: true}}
	ctx := context.Background()
	loop.Handle(ctx, task(1, wire.TaskKindBackupNow, `{"job_id":5}`))
	if r := api.results[1]; r.status != wire.TaskStatusDone || r.result != `{"job_id":5,"already_running":true}` {
		t.Fatalf("busy %+v", r)
	}
	// No keystore wired: key tasks fail with a key result.
	loop.Handle(ctx, task(2, wire.TaskKindKeyShare, `{}`))
	if r := api.results[2]; r.kind != "key_share" || r.key == nil || r.key.Error == "" {
		t.Fatalf("no keystore %+v", r)
	}
	// Shutdown before the result: nothing is posted (the lease requeues it).
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	loop.Handle(cctx, task(3, wire.TaskKindBackupNow, `{"job_id":5}`))
	if _, ok := api.results[3]; ok {
		t.Fatal("result posted after shutdown")
	}
}
