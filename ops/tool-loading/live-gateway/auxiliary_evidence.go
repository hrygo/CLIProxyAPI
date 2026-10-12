package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

func closeAuxiliaryWork(ctx context.Context, scope *helps.HTTPAuxiliaryWorkScope, directory string) error {
	errJoin := scope.CloseContext(ctx)
	state := scope.Snapshot()
	file, err := os.OpenFile(filepath.Join(directory, "auxiliary-work.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.Join(errJoin, err)
	}
	err = json.NewEncoder(file).Encode(state)
	if err == nil {
		err = file.Sync()
	}
	if !state.Sealed || state.Active != 0 || state.Started != state.Completed || state.Rejected != 0 {
		err = errors.Join(err, errors.New("auxiliary HTTP work settlement incomplete"))
	}
	return errors.Join(errJoin, err, file.Close())
}
