package common

import (
	"context"
	"log"
	"time"

	pb "github.com/perillaroc/takler-client/takler_protocol"
)

func (c *TaklerServiceClient) RunCommandInit(nodePath string, taskId string) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandInit(ctx, &pb.InitCommand{
			ChildOptions: &pb.ChildCommandOptions{
				NodePath: nodePath,
			},
			TaskId: taskId,
		})

		if err != nil {
			log.Fatalf("could not init: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}

func (c *TaklerServiceClient) RunCommandComplete(nodePath string) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandComplete(ctx, &pb.CompleteCommand{
			ChildOptions: &pb.ChildCommandOptions{
				NodePath: nodePath,
			},
		})

		if err != nil {
			log.Fatalf("could not init: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}

func (c *TaklerServiceClient) RunCommandAbort(nodePath string, reason string) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandAbort(ctx, &pb.AbortCommand{
			ChildOptions: &pb.ChildCommandOptions{
				NodePath: nodePath,
			},
			Reason: reason,
		})

		if err != nil {
			log.Fatalf("could not init: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}

func (c *TaklerServiceClient) RunCommandEvent(nodePath string, eventName string) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandEvent(ctx, &pb.EventCommand{
			ChildOptions: &pb.ChildCommandOptions{
				NodePath: nodePath,
			},
			EventName: eventName,
		})

		if err != nil {
			log.Fatalf("could not init: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}

func (c *TaklerServiceClient) RunCommandMeter(nodePath string, meterName string, meterValue string) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandMeter(ctx, &pb.MeterCommand{
			ChildOptions: &pb.ChildCommandOptions{
				NodePath: nodePath,
			},
			MeterName:  meterName,
			MeterValue: meterValue,
		})

		if err != nil {
			log.Fatalf("could not init: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}
