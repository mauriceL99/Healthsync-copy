package agent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/client"
	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
)

// simulatorChunkDays mirrors SyncPlan.chunkDays in the iOS app
// (ios/HealthBridge/Sync/SyncEngine.swift) so uploads look like the real app's.
const simulatorChunkDays = 30

// SimulateOptions configures `healthbridge dev simulate-device`.
type SimulateOptions struct {
	PairingCode string
	Days        int
	Timezone    string
	Seed        int64
	Unlink      bool
}

// SimulateDevice acts like the iPhone app with synthetic data: it links a
// throwaway device with the pairing code, encrypts a backfill for the agent
// key and uploads it. It is meant for demos and end-to-end tests.
func SimulateDevice(ctx context.Context, env Env, o SimulateOptions) error {
	p, err := protocol.DecodePairing(o.PairingCode)
	if err != nil {
		return err
	}
	secret, _ := protocol.DecodeB64U(p.Secret, 32)
	encBytes, _ := protocol.DecodeB64(p.Enc, 32)
	recipient, err := ecdh.X25519().NewPublicKey(encBytes)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(firstNonEmpty(o.Timezone, "Europe/Berlin"))
	if err != nil {
		return err
	}
	devPub, devPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	c, err := client.New(p.Relay, protocol.KeyNewDevice, devPriv, env.timeout())
	if err != nil {
		return err
	}
	c.Now = env.now
	deviceID, err := c.LinkDevice(ctx, p.Account, secret, devPub, "HealthBridge Simulator", "simulator", "dev")
	if err != nil {
		if client.IsCode(err, "device_already_linked") {
			return fmt.Errorf("%w (run `healthbridge unlink` first)", err)
		}
		return err
	}
	dev := c.WithKey(protocol.DeviceKey(deviceID))
	safety, _ := p.SafetyCode()
	fmt.Fprintf(env.Out, "Simulated device %s linked to %q (safety code %s).\n", deviceID, p.Name, safety)

	if o.Days <= 0 {
		o.Days = 30
	}
	batch := healthdata.Synthetic(env.now(), o.Days, loc, o.Seed)
	chunks := chunkBatch(batch, simulatorChunkDays)
	var envelopes []protocol.Envelope
	for i, chunk := range chunks {
		plain, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		kind := protocol.KindBackfill
		if i == len(chunks)-1 {
			kind = protocol.KindSync
		}
		e, err := protocol.Seal(plain, recipient, protocol.SealParams{Account: p.Account, Kind: kind, CreatedAt: env.now()})
		if err != nil {
			return err
		}
		envelopes = append(envelopes, e)
	}
	seqs, err := dev.Upload(ctx, envelopes)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Uploaded %d encrypted envelopes (%d synthetic days, seq %d–%d).\n", len(seqs), o.Days, seqs[0], seqs[len(seqs)-1])
	if o.Unlink {
		if err := dev.UnlinkSelf(ctx); err != nil {
			return err
		}
		fmt.Fprintln(env.Out, "Simulated device unlinked again.")
	}
	return nil
}

// chunkBatch splits a synthetic batch into batches of at most n days, with
// each workout in the chunk that contains its day.
func chunkBatch(batch map[string]any, n int) []map[string]any {
	days, _ := batch["days"].(map[string]any)
	workouts, _ := batch["workouts"].([]any)
	dates := make([]string, 0, len(days))
	for d := range days {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	var out []map[string]any
	for start := 0; start < len(dates); start += n {
		end := min(start+n, len(dates))
		chunk := map[string]any{}
		for k, v := range batch {
			chunk[k] = v
		}
		part := map[string]any{}
		for _, d := range dates[start:end] {
			part[d] = days[d]
		}
		var ws []any
		for _, w := range workouts {
			startTime, _ := w.(map[string]any)["start"].(string)
			t, err := time.Parse(time.RFC3339, startTime)
			if err != nil {
				continue
			}
			local := t.In(time.Local).Format("2006-01-02")
			if tz, ok := batch["timezone"].(string); ok {
				if loc, err := time.LoadLocation(tz); err == nil {
					local = t.In(loc).Format("2006-01-02")
				}
			}
			if local >= dates[start] && local <= dates[end-1] {
				ws = append(ws, w)
			}
		}
		chunk["days"], chunk["workouts"] = part, ws
		out = append(out, chunk)
	}
	return out
}
