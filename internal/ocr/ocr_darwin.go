package ocr

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS 10.15+ has the Vision framework; JavaScript for Automation can call it, so no
// helper binary is needed.
const jxa = `ObjC.import('Foundation'); ObjC.import('Vision');
function run(argv) {
  const url = $.NSURL.fileURLWithPath(argv[0]);
  const req = $.VNRecognizeTextRequest.alloc.init;
  req.recognitionLevel = 0; // accurate
  req.usesLanguageCorrection = true;
  const handler = $.VNImageRequestHandler.alloc.initWithURLOptions(url, $({}));
  const err = Ref();
  if (!handler.performRequestsError($([req]), err)) throw new Error('Vision could not read the image');
  const res = req.results, out = [];
  for (let i = 0; i < res.count; i++) {
    const c = res.objectAtIndex(i).topCandidates(1);
    if (c.count > 0) out.push(c.objectAtIndex(0).string.js);
  }
  return out.join('\n');
}`

func recognise(ctx context.Context, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	c := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", jxa, abs)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("macOS Vision: " + msg)
	}
	return out.String(), nil
}

func engine() (string, string) { return "macOS Vision", "Apple's on-device text recognition" }
