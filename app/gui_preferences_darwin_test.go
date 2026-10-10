//go:build gui && darwin && cgo

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGUIPreferencesDisablePressAndHoldWithoutSaving(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	harness := filepath.Join(directory, "preferences.m")
	binary := filepath.Join(directory, "preferences")
	source := `#import <Foundation/Foundation.h>
#include <stdio.h>

void autodoc_configure_gui_preferences(void);

int main(void) {
	@autoreleasepool {
		NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
		NSString *domain = [[NSProcessInfo processInfo] processName];
		NSDictionary *saved = [defaults persistentDomainForName:domain];
		[defaults setVolatileDomain:@{@"ApplePressAndHoldEnabled": @YES} forName:NSGlobalDomain];
		[defaults setVolatileDomain:@{@"ApplePressAndHoldEnabled": @YES, @"UnrelatedArgument": @YES} forName:NSArgumentDomain];
		autodoc_configure_gui_preferences();
		if ([defaults boolForKey:@"ApplePressAndHoldEnabled"]) {
			fprintf(stderr, "press-and-hold still enabled\n");
			return 1;
		}
		if (![defaults boolForKey:@"UnrelatedArgument"]) {
			fprintf(stderr, "unrelated argument was lost\n");
			return 1;
		}
		NSDictionary *after = [defaults persistentDomainForName:domain];
		if (saved != after && ![saved isEqualToDictionary:after]) {
			fprintf(stderr, "persistent preferences changed\n");
			return 1;
		}
		if (![[defaults volatileDomainForName:NSGlobalDomain][@"ApplePressAndHoldEnabled"] boolValue]) {
			fprintf(stderr, "global preference changed\n");
			return 1;
		}
	}
	return 0;
}
`
	if err := os.WriteFile(harness, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	compile := exec.Command("/usr/bin/clang", "-framework", "Foundation", "gui_preferences_darwin.m", harness, "-o", binary)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile preferences harness: %v\n%s", err, output)
	}
	if output, err := exec.Command(binary).CombinedOutput(); err != nil {
		t.Fatalf("preferences harness: %v\n%s", err, output)
	}
}
