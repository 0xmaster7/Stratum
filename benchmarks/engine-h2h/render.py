#!/usr/bin/env python3
import json,pathlib,sys
root=pathlib.Path(sys.argv[1]);data=json.loads((root/'comparison.json').read_text())
template=pathlib.Path(__file__).with_name('template.html').read_text()
(root/'comparison.html').write_text(template.replace('/*DATA*/{}',json.dumps(data).replace('</','<\\/')))
