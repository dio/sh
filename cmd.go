package sh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// RunCmd returns a function that will call Run with the given command. This is
// useful for creating command aliases to make your scripts easier to read, like
// this:
//
//	 // in a helper file somewhere
//	 var g0 = sh.RunCmd("go")  // go is a keyword :(
//
//	 // somewhere in your main code
//		if err := g0("install", "github.com/gohugo/hugo"); err != nil {
//			return err
//	 }
//
// Args passed to command get baked in as args to the command when you run it.
// Any args passed in when you run the returned function will be appended to the
// original args.  For example, this is equivalent to the above:
//
//	var goInstall = sh.RunCmd("go", "install") goInstall("github.com/gohugo/hugo")
//
// RunCmd uses Exec underneath, so see those docs for more details.
func RunCmd(ctx context.Context, cmd string, args ...string) func(args ...string) error {
	return func(args2 ...string) error {
		return Run(ctx, cmd, append(args, args2...)...)
	}
}

// OutCmd is like RunCmd except the command returns the output of the
// command.
func OutCmd(ctx context.Context, cmd string, args ...string) func(args ...string) (string, error) {
	return func(args2 ...string) (string, error) {
		return Output(ctx, cmd, append(args, args2...)...)
	}
}

// Run is like RunWith, but doesn't specify any environment variables.
func Run(ctx context.Context, cmd string, args ...string) error {
	return RunWith(ctx, nil, cmd, args...)
}

// RunV is like Run, but always sends the command's stdout to os.Stdout.
func RunV(ctx context.Context, cmd string, args ...string) error {
	_, err := Exec(ctx, nil, os.Stdout, os.Stderr, cmd, args...)
	return err
}

// RunWith runs the given command, directing stderr to this program's stderr and
// printing stdout to stdout if mage was run with -v.  It adds adds env to the
// environment variables for the command being run. Environment variables should
// be in the format name=value.
func RunWith(ctx context.Context, env map[string]string, cmd string, args ...string) error {
	_, err := Exec(ctx, env, io.Discard, os.Stderr, cmd, args...)
	return err
}

// RunWithV is like RunWith, but always sends the command's stdout to os.Stdout.
func RunWithV(ctx context.Context, env map[string]string, cmd string, args ...string) error {
	_, err := Exec(ctx, env, os.Stdout, os.Stderr, cmd, args...)
	return err
}

// Output runs the command and returns the text from stdout.
func Output(ctx context.Context, cmd string, args ...string) (string, error) {
	buf := &bytes.Buffer{}
	_, err := Exec(ctx, nil, buf, os.Stderr, cmd, args...)
	return strings.TrimSuffix(buf.String(), "\n"), err
}

// OutputWith is like RunWith, but returns what is written to stdout.
func OutputWith(ctx context.Context, env map[string]string, cmd string, args ...string) (string, error) {
	buf := &bytes.Buffer{}
	_, err := Exec(ctx, env, buf, os.Stderr, cmd, args...)
	return strings.TrimSuffix(buf.String(), "\n"), err
}

// Exec executes the command, piping its stdout and stderr to the given
// writers. If the command fails, it will return an error that, if returned
// from a target or mg.Deps call, will cause mage to exit with the same code as
// the command failed with. Env is a list of environment variables to set when
// running the command, these override the current environment variables set
// (which are also passed to the command). cmd and args may include references
// to environment variables in $FOO format, in which case these will be
// expanded before the command is run.
//
// Ran reports if the command ran (rather than was not found or not executable).
// Code reports the exit code the command returned if it ran. If err == nil, ran
// is always true and code is always 0.
func Exec(ctx context.Context, env map[string]string, stdout, stderr io.Writer, cmd string, args ...string) (ran bool, err error) {
	return ExecWithStdin(ctx, env, os.Stdin, stdout, stderr, cmd, args...)
}

func ExecWithStdin(ctx context.Context, env map[string]string, stdin io.Reader, stdout, stderr io.Writer, cmd string, args ...string) (ran bool, err error) {
	expand := func(s string) string {
		s2, ok := env[s]
		if ok {
			return s2
		}
		return os.Getenv(s)
	}
	cmd = os.Expand(cmd, expand)
	for i := range args {
		args[i] = os.Expand(args[i], expand)
	}
	ran, code, err := run(ctx, env, stdin, stdout, stderr, cmd, args...)
	if err == nil {
		return true, nil
	}
	if ran {
		return ran, Fatalf(code, `running "%s %s" failed with exit code %d`, cmd, strings.Join(args, " "), code)
	}
	return ran, fmt.Errorf(`failed to run "%s %s: %v"`, cmd, strings.Join(args, " "), err)
}

func run(ctx context.Context, env map[string]string, stdin io.Reader, stdout, stderr io.Writer, cmd string, args ...string) (ran bool, code int, err error) {
	c := exec.CommandContext(ctx, cmd, args...)
	c.Env = os.Environ()
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
	c.Stderr = stderr
	c.Stdout = stdout
	c.Stdin = stdin

	err = c.Run()
	return CmdRan(err), ExitStatus(err), err
}

// CmdRan examines the error to determine if it was generated as a result of a
// command running via os/exec.Command.  If the error is nil, or the command ran
// (even if it exited with a non-zero exit code), CmdRan reports true.  If the
// error is an unrecognized type, or it is an error from exec.Command that says
// the command failed to run (usually due to the command not existing or not
// being executable), it reports false.
func CmdRan(err error) bool {
	if err == nil {
		return true
	}
	ee, ok := err.(*exec.ExitError)
	if ok {
		return ee.Exited()
	}
	return false
}

type exitStatus interface {
	ExitStatus() int
}

// ExitStatus returns the exit status of the error if it is an exec.ExitError
// or if it implements ExitStatus() int.
// 0 if it is nil or 1 if it is a different error.
func ExitStatus(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(exitStatus); ok {
		return e.ExitStatus()
	}
	if e, ok := err.(*exec.ExitError); ok {
		if ex, ok := e.Sys().(exitStatus); ok {
			return ex.ExitStatus()
		}
	}
	return 1
}

// Fatalf returns an error that will cause mage to print out the
// given message and exit with the given exit code.
func Fatalf(code int, format string, args ...interface{}) error {
	return fatalErr{
		code:  code,
		error: fmt.Errorf(format, args...),
	}
}

type fatalErr struct {
	code int
	error
}

func (f fatalErr) ExitStatus() int {
	return f.code
}
