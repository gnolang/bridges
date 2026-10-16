package agent

import "context"

// The inbound half registered as a job of the relayer (E-M3's RegisterRelayerJob, relayer.go): one binary, one tick,
// one state file — the job's state under its name.

func init() { RegisterRelayerJob(func() RelayerJob { return &inboundRelayerJob{} }) }

type inboundRelayerJob struct{ inboundJob }

func (j *inboundRelayerJob) Name() string { return "inbound" }

func (j *inboundRelayerJob) State() any { return &j.st }

// Tick runs the inbound pass through the relayer: its config, its Ethereum provider, its gno node and gas key, its
// primary/backup rule and attempt bookkeeping.
func (j *inboundRelayerJob) Tick(ctx context.Context, r *Relayer) error {
	j.cfg, j.eth, j.alarm = r.Cfg, r.Eth, r.Alarm
	return j.tick(ctx, r)
}
