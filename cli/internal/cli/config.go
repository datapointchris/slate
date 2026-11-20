package cli

import (
	"errors"
	"fmt"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goclilogin"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/config"
)

func newConfigCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "config",
		Short: "Where this machine finds the API",
		Long: "The API's address, its login server and this machine's client, from one\n" +
			"TOML file, $XDG_CONFIG_HOME/slate/config.toml. `slate auth login <url>`\n" +
			"fills in whatever it lacks.\n" +
			"\n" +
			"Its keys:\n" +
			"  api            the API's URL; --api outranks it\n" +
			"  issuer         the login server that issues the API's tokens\n" +
			"  client_id      this machine's client; login writes slate-cli-<short\n" +
			"                 hostname> where none is given\n" +
			"  token_command  a program that hands slate its tokens, as a list of\n" +
			"                 arguments: token_command = [\"my-helper\", \"--slate\"].\n" +
			"                 It prints an OAuth token response on stdout:\n" +
			"                 {\"access_token\": \"...\", \"token_type\": \"Bearer\",\n" +
			"                 \"expires_in\": 3600}. token_type and expires_in may be\n" +
			"                 left out, and a token_type other than Bearer is refused.\n" +
			"                 A file setting it needs only api, and never logs in.\n" +
			"\n" +
			"SLATE_CLIENT_SECRET in the environment runs slate as a service, through the\n" +
			"client-credentials grant for the file's client_id. It is never written to\n" +
			"the file, and slate refuses it beside a token_command.",
		Example: "  slate config show  each setting, and whether the file or a flag set it\n" +
			"  slate config path  where the file goes, whether or not it exists",
	})
	cmd.AddCommand(withAPIFlag(newConfigShowCommand()), newConfigPathCommand())
	return cmd
}

func newConfigShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show each setting and where it came from",
		Example: "  slate config show         each setting, and whether the file or a flag set it\n" +
			"  slate config show --json  the same, for a script",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deployment, err := config.Load(cmd.Context(), apiOverride)
			if err != nil && !errors.Is(err, goclilogin.ErrNotConfigured) {
				return err
			}
			if asJSON {
				if err := encodeJSON(cmd.OutOrStdout(), deployment); err != nil {
					return err
				}
			} else {
				goclilogin.WriteDeployment(cmd.OutOrStdout(), deployment)
			}
			if err != nil {
				// stderr, so a --json caller's stdout stays one parsable document.
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "\n"+err.Error())
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the settings as JSON to stdout")
	return cmd
}

func newConfigPathCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "path",
		Short:   "Print the config file's path",
		Example: "  slate config path  where the file goes, whether or not it exists",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := config.Path()
			if path == "" {
				return errors.New("no config file: neither XDG_CONFIG_HOME nor a home directory is set")
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}
