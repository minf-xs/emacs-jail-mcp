// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package cli

import (
	"github.com/spf13/cobra"

	"github.com/gavv/emacs-jail-mcp/internal/container"
)

func newEntrypointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "entrypoint",
		Short:  "Run the container entrypoint",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return container.NewEntrypoint().Run()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return cmd
}
