package common

import (
	"context"
	"log"
	"time"

	pb "github.com/perillaroc/takler-client/takler_protocol"
)

func (c *TaklerServiceClient) RunCommandRequeue(nodePaths []string) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandRequeue(ctx, &pb.RequeueCommand{
			NodePath: nodePaths,
		})

		if err != nil {
			log.Fatalf("could not requeue: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}

func (c *TaklerServiceClient) RunCommandSuspend(nodePaths []string) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandSuspend(ctx, &pb.SuspendCommand{
			NodePath: nodePaths,
		})

		if err != nil {
			log.Fatalf("could not suspend: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}

func (c *TaklerServiceClient) RunCommandResume(nodePaths []string) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandResume(ctx, &pb.SuspendCommand{
			NodePath: nodePaths,
		})

		if err != nil {
			log.Fatalf("could not resume: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}

func (c *TaklerServiceClient) RunCommandRun(nodePaths []string, force bool) error {
	return c.withConnection(func(client pb.TaklerServerClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r, err := client.RunCommandRun(ctx, &pb.RunCommand{
			NodePath: nodePaths,
			Force:    force,
		})

		if err != nil {
			log.Fatalf("could not run: %v", err)
		}

		log.Printf("%d", r.GetFlag())
		return nil
	})
}
