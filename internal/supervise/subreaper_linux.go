package supervise

import "syscall"

const prSetChildSubreaper = 36

func BecomeSubreaper() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); errno != 0 {
		return errno
	}

	return nil
}
