package nacos

import "github.com/go-lynx/lynx"

func currentLynxApp() *lynx.LynxApp {
	return lynx.Lynx()
}

func currentLynxName() string {
	return lynx.GetName()
}
