package httpclient

import (
	"github.com/xiaozhou26/re-tlsclient/chrome"
)

// Chrome 构造 chrome 浏览器指纹 client (默认 V148 / MacOS)。
func Chrome() (HTTPClient, error) {
	return chrome.NewClient(chrome.V148, chrome.MacOS)
}

// ChromeWith 构造指定 version+platform 的 chrome 浏览器指纹 client。
func ChromeWith(v chrome.Version, p chrome.Platform) (HTTPClient, error) {
	return chrome.NewClient(v, p)
}
