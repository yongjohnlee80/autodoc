//go:build gui && darwin && cgo

#import <Foundation/Foundation.h>

void autodoc_configure_gui_preferences(void) {
	@autoreleasepool {
		NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
		NSMutableDictionary *arguments = [[defaults volatileDomainForName:NSArgumentDomain] mutableCopy];
		arguments[@"ApplePressAndHoldEnabled"] = @NO;
		[defaults setVolatileDomain:arguments forName:NSArgumentDomain];
		[arguments release];
	}
}
