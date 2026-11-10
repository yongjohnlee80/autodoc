// Package userpermtest is a test's way to make a file that internal/userperm counts as exposed:
// readable by others, as each OS says it (on Unix a group or other permission bit, on Windows an
// access entry for others in the file's DACL).
package userpermtest
