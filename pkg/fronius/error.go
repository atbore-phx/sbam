package fronius

import u "sbam/src/utils"

func handleError(err error, msg string) error {
	if err != nil {
		u.Log.Errorf(msg+" %s", err)
		return err
	}
	return nil
}

func handleErrorPanic(err error, msg string) error {
	if err != nil {
		u.Log.Errorf(msg+" %s", err)
		panic(err)
	}
	return nil
}
