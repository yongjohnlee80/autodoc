//go:build gui && darwin && cgo

package app

/*
#cgo LDFLAGS: -framework Foundation
void autodoc_configure_gui_preferences(void);
*/
import "C"

func configureGUIPreferences() {
	C.autodoc_configure_gui_preferences()
}
