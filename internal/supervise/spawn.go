package supervise

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

func (s *Supervisor) spawn(name string, spec spec) (*process, error) {
	identity, err := identityOf(spec.user)
	if err != nil {
		return nil, err
	}
	output, drained, err := s.logs.pipe(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Close() }()
	cmd := exec.Command(spec.argv[0], spec.argv[1:]...)
	cmd.Dir = spec.workdir
	cmd.Env = append(append(os.Environ(), identity.env...), envList(spec.env)...)
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: identity.credential}

	proc, err := s.reaper.start(cmd)
	if err != nil {
		return nil, err
	}
	proc.drained = drained

	return proc, nil
}

func envList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for key, value := range env {
		list = append(list, key+"="+value)
	}

	return list
}

type identity struct {
	credential *syscall.Credential
	env        []string
}

func identityOf(spec string) (identity, error) {
	if spec == "" {
		return identity{}, nil
	}
	name, group, hasGroup := strings.Cut(spec, ":")
	account, err := lookupUser(name)
	if err != nil {
		return identity{}, err
	}
	gid := account.gid
	if hasGroup {
		if gid, err = lookupGroup(group); err != nil {
			return identity{}, err
		}
	}

	return identity{
		credential: &syscall.Credential{Uid: account.uid, Gid: gid, Groups: account.groups},
		env:        account.env(),
	}, nil
}

type account struct {
	name   string
	home   string
	uid    uint32
	gid    uint32
	groups []uint32
}

func (a account) env() []string {
	if a.name == "" {
		return nil
	}

	return []string{"HOME=" + a.home, "USER=" + a.name, "LOGNAME=" + a.name}
}

func lookupUser(name string) (account, error) {
	found, err := findUser(name)
	if err != nil {
		uid, numeric := parseID(name)
		if !numeric {
			return account{}, fmt.Errorf("unknown user %q: %w", name, err)
		}

		return account{uid: uid, gid: uid}, nil
	}
	uid, _ := parseID(found.Uid)
	gid, _ := parseID(found.Gid)

	return account{name: found.Username, home: found.HomeDir, uid: uid, gid: gid, groups: supplementaryGroups(found)}, nil
}

func findUser(name string) (*user.User, error) {
	if _, numeric := parseID(name); numeric {
		return user.LookupId(name)
	}

	return user.Lookup(name)
}

func supplementaryGroups(found *user.User) []uint32 {
	ids, err := found.GroupIds()
	if err != nil {
		return nil
	}
	groups := make([]uint32, 0, len(ids))
	for _, id := range ids {
		if gid, ok := parseID(id); ok {
			groups = append(groups, gid)
		}
	}

	return groups
}

func lookupGroup(name string) (uint32, error) {
	if gid, numeric := parseID(name); numeric {
		return gid, nil
	}
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("unknown group %q: %w", name, err)
	}
	gid, _ := parseID(group.Gid)

	return gid, nil
}

func parseID(text string) (uint32, bool) {
	id, err := strconv.ParseUint(text, 10, 32)

	return uint32(id), err == nil
}
