package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift/hypershift/support/releaseinfo/fixtures"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/spf13/cobra"
)

func main() {
	cmd := NewCommand()
	_ = cmd.Execute()
}

type Options struct {
	Image      string
	Iterations int
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "registryclient-test",
		Short:        "Tests registry client release info provider",
		SilenceUsage: true,
	}

	opts := &Options{}

	cmd.Flags().StringVar(&opts.Image, "image", opts.Image, "Image to pull")
	cmd.Flags().IntVar(&opts.Iterations, "iterations", opts.Iterations, "Number of times to extract files from the image")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := opts.Extract(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(2)
		}
	}
	return cmd
}

const (
	emptyPullSecret = `{"auths":{}}`

	imageRefsFile  = "manifests/image-references"
	bootImagesFile = "manifests/boot-images.yaml"
)

func (o *Options) Extract(ctx context.Context) error {
	fmt.Printf("The number of iterations is %d\n", o.Iterations)
	fmt.Printf("The image to extract is %s\n", o.Image)
	for i := 0; i < o.Iterations; i++ {
		fmt.Printf("%v: Running iteration %d\n", time.Now(), i+1)
		result, err := registryclient.ExtractImageFiles(ctx, o.Image, []byte(emptyPullSecret), imageRefsFile, bootImagesFile)
		if err != nil {
			return fmt.Errorf("failed to extract files from image: %w", err)
		}
		if !bytes.Equal(fixtures.ImageReferencesJSON_4_8, result[imageRefsFile]) {
			return fmt.Errorf("unexpected image refs file:\n%s", string(result[imageRefsFile]))
		}
		if !bytes.Equal(fixtures.CoreOSBootImagesYAML_4_8, result[bootImagesFile]) {
			return fmt.Errorf("unexpected boot images file:\n%s", string(result[bootImagesFile]))
		}
	}
	fmt.Printf("Test Complete\n")
	return nil
}
