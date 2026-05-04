package utils

// HandleError logs non-fatal errors and returns them.
func HandleError(err error, msg string) error {
	if err != nil {
		Log.Errorf(msg+" %s", err)
		return err
	}
	return nil
}

// HandleErrorPanic logs the error and panics when err is non-nil.
func HandleErrorPanic(err error, msg string) error {
	if err != nil {
		Log.Errorf(msg+" %s", err)
		panic(err)
	}
	return nil
}
