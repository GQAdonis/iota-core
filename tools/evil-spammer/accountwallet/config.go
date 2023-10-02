package accountwallet

import (
	"encoding/json"
	"os"

	"github.com/iotaledger/hive.go/ds/types"
	iotago "github.com/iotaledger/iota.go/v4"
)

// commands

const (
	CreateAccountCommand  = "create"
	DestroyAccountCommand = "destroy"
	AllotAccountCommand   = "allot"
	ListAccountsCommand   = "list"
)

var AvailableCommands = map[string]types.Empty{
	CreateAccountCommand:  types.Void,
	DestroyAccountCommand: types.Void,
	AllotAccountCommand:   types.Void,
	ListAccountsCommand:   types.Void,
}

type Configuration struct {
	BindAddress       string `json:"bindAddress,omitempty"`
	AccountStatesFile string `json:"accountStatesFile,omitempty"`
}

var accountConfigFile = "account_config.json"

var accountConfigJSON = `{
	"bindAddress": "http://localhost:8080",
	"accountStatesFile": "wallet.LOCK"
}`

// loadAccountConfig loads the config file.
func loadAccountConfig() *Configuration {
	// open config file
	config := new(Configuration)
	file, err := os.Open(accountConfigFile)
	if err != nil {
		if !os.IsNotExist(err) {
			panic(err)
		}

		//nolint:gosec // users should be able to read the file
		if err = os.WriteFile(accountConfigFile, []byte(accountConfigJSON), 0o644); err != nil {
			panic(err)
		}
		if file, err = os.Open(accountConfigFile); err != nil {
			panic(err)
		}
	}
	defer file.Close()

	// decode config file
	if err = json.NewDecoder(file).Decode(config); err != nil {
		panic(err)
	}

	return config
}

type CreateAccountParams struct {
	Alias  string
	Amount uint64
}

type DestroyAccountParams struct {
	AccountAlias string
}

type AllotAccountParams struct {
	Amount uint64
	To     string
	From   string // if not set we use faucet
}

// TODO do wee need to restrict that only one instance of the wallet runs?
type StateData struct {
	Accounts []Account `serix:"0,mapKey=accounts,lengthPrefixType=uint8"`
	// TODO: other info that the account wallet needs to store
}

type Account struct {
	Alias     string           `serix:"0,mapKey=alias"`
	AccountID iotago.AccountID `serix:"1,mapKey=accountID"`
	// TODO: other info of an account
}
