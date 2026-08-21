package common

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "github.com/perillaroc/takler-client/takler_protocol"
)

func (c *TaklerServiceClient) RunQueryShow(
	showTrigger bool,
	showParameter bool,
	showLimit bool,
	showEvent bool,
	showMeter bool,
) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunRequestShow(ctx, &pb.ShowRequest{
			ShowTrigger:   showTrigger,
			ShowParameter: showParameter,
			ShowLimit:     showLimit,
			ShowEvent:     showEvent,
			ShowMeter:     showMeter,
		})

		if err != nil {
			log.Fatalf("could not init: %v", err)
		}

		fmt.Print(r.GetOutput())
		return nil
	})
}

func (c *TaklerServiceClient) RunQueryPing() error {
	// The measured duration covers building the connection as well, as that is
	// the part a ping is meant to prove.
	startTime := time.Now()

	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.RunRequestPing(ctx, &pb.PingRequest{})

		if err != nil {
			log.Fatalf("ping server (%s:%s) failed: %v", c.Host, c.Port, err)
		}

		endTime := time.Now()
		d := endTime.Sub(startTime)

		fmt.Printf("ping server (%s:%s) succeeded in %v\n", c.Host, c.Port, d)
		return nil
	})
}
