package cli

import (
	"fmt"

	"github.com/9seconds/mtg/v2/mtglib"
)

type GenerateUserSecret struct {
	Secret   string `kong:"arg,required,help='Proxy secret.'"`
	UserUUID string `kong:"arg,required,help='User UUID.'"`
	Hex      bool   `kong:"help='Print secret in hex encoding.',short='x'"`
}

func (g *GenerateUserSecret) Run(cli *CLI, _ string) error {
	secret := mtglib.ResetHost(cli.GenerateUserSecret.Secret, cli.GenerateUserSecret.UserUUID)

	if g.Hex {
		fmt.Println(secret.Hex()) //nolint: forbidigo
	} else {
		fmt.Println(secret.Base64()) //nolint: forbidigo
	}

	return nil
}
