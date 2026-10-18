package profiles

import (
	"context"
	"sort"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// ReportSender sends PUT /device/profiles (injected; the agent's device
// client or outbox).
type ReportSender func(ctx context.Context, rep wire.ProfileReport) error

// BuildReport builds the PUT /device/profiles body from local records:
// every profile except StateDiscovered ones, and a consent record (never the
// blob, only its digest) for each profile in a consenting state. Output is
// sorted by SID.
func BuildReport(recs []Record) wire.ProfileReport {
	rep := wire.ProfileReport{Profiles: []wire.ProfileReportEntry{}, Consents: []wire.ProfileConsentRecord{}}
	sorted := append([]Record(nil), recs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SID < sorted[j].SID })
	for _, r := range sorted {
		if r.State == StateDiscovered || r.State == "" {
			continue
		}
		state := r.State
		if Consented(state) && !r.Allowed() {
			// A consenting state without a proof is never claimed: the
			// server would reject it (no consent record).
			continue
		}
		rep.Profiles = append(rep.Profiles, wire.ProfileReportEntry{
			SID: r.SID, Username: r.Username, ProfilePath: r.ProfilePath, State: state,
		})
		if Consented(state) {
			rep.Consents = append(rep.Consents, wire.ProfileConsentRecord{
				ConsentID: r.ConsentID, SID: r.SID, ConsentAt: r.ConsentAt, ProofDigest: r.ProofDigest,
			})
		}
	}
	return rep
}
