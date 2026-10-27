//go:build !linux

package supervise

func BecomeSubreaper() error {
	return nil
}
