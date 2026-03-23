package cmd

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var workers int
var rate int
var durationStr string
var totalSpans int

var rootCmd = &cobra.Command{
    Use:   "stress",
    Short: "OTel pipeline stress tester",
    RunE: func(cmd *cobra.Command, args []string) error {
        d, err := time.ParseDuration(durationStr)
        if err != nil {
            return fmt.Errorf("invalid duration: %w", err)
        }

        // init telemetry
        tp, err := initTracer(cmd.Context())
        if err != nil {
            return fmt.Errorf("init tracer: %w", err)
        }
        defer func() {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            _ = tp.Shutdown(ctx)
        }()

        runStress(totalSpans, workers, rate, d)
        return nil
    },
}

func Execute() error {
    return rootCmd.Execute()
}

func init() {
    rootCmd.Flags().IntVar(&workers, "workers", 1, "number of goroutines to spawn")
    rootCmd.Flags().IntVar(&rate, "rate", 1, "spans per second per worker (used if --spans not set)")
    rootCmd.Flags().StringVar(&durationStr, "duration", "10s", "how long to run (e.g. 30s,1m)")
    rootCmd.Flags().IntVar(&totalSpans, "spans", 0, "total spans to emit (0 = use rate)")
}

func initTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
    exp, err := otlptracegrpc.New(ctx,
        otlptracegrpc.WithEndpoint("localhost:4317"),
        otlptracegrpc.WithInsecure(),
    )
    if err != nil {
        return nil, err
    }
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithSampler(sdktrace.AlwaysSample()),
        sdktrace.WithBatcher(exp),
    )
    otel.SetTracerProvider(tp)
    return tp, nil
}

// runStress spawns `workers` goroutines and emits either `totalSpans` across
// all workers within `duration`, or runs at `rate` spans/sec per worker if
// totalSpans==0.
func runStress(totalSpans, workers, rate int, duration time.Duration) {
    if workers <= 0 {
        workers = 1
    }
    if rate <= 0 {
        rate = 1
    }

    ctx, cancel := context.WithTimeout(context.Background(), duration)
    defer cancel()

    var wg sync.WaitGroup
    var sent int64

    var interval time.Duration
    if totalSpans > 0 {
        perWorkerRate := float64(totalSpans) / (float64(workers) * duration.Seconds())
        // add a small cushion to account for startup/shutdown
        perWorkerRate = perWorkerRate * 1.05
        if perWorkerRate <= 0 {
            perWorkerRate = 1
        }
        interval = time.Duration(float64(time.Second) / perWorkerRate)
        if interval < time.Nanosecond {
            interval = time.Nanosecond
        }
    } else {
        interval = time.Second / time.Duration(rate)
    }

    tr := otel.Tracer("stressor")

    for i := 0; i < workers; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for {
                if ctx.Err() != nil {
                    return
                }
                if totalSpans > 0 && atomic.LoadInt64(&sent) >= int64(totalSpans) {
                    return
                }

                _, span := tr.Start(ctx, "stress.span")
                span.End()
                atomic.AddInt64(&sent, 1)

                select {
                case <-ctx.Done():
                    return
                default:
                    time.Sleep(interval)
                }
            }
        }(i)
    }

    wg.Wait()
    log.Printf("Finished. Total spans emitted: %d\n", atomic.LoadInt64(&sent))
}
