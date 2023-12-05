package indexer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/civilware/Gnomon/structures"
)

func (indexer *Indexer) PipeFilter(line_parts []string, invokedetails []*structures.SCTXParse) (details_filtered []*structures.SCTXParse) {
	for _, invoke := range invokedetails {
		if len(line_parts) == 3 {
			ca, _ := strconv.Atoi(line_parts[2])
			if invoke.Height >= int64(ca) {
				details_filtered = append(details_filtered, invoke)
			}
		} else {
			if len(line_parts) >= 3 && line_parts[2] == "|" {
				if len(line_parts) > 4 {
					// Here we assume there's some modern language usage to filter data which we can manipulate *how* we sort data
					// Example: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
					// Note - Includes references on indexes to d6ad... of string a9bf71...
					// Example2: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | grep a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
					// Note - Includes references on indexes to d6ad... of string a9bf71...
					// Example3: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | exclude a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
					// Note - Excludes references on indexes to d6ad... of string a9bf71...
					// TODO: More elegant + various data type sifting and other. Reference - https://github.com/deroproject/derohe/blob/main/cmd/dero-wallet-cli/easymenu_post_open.go#L290
					switch line_parts[3] {
					case "grep":
						if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[4:], " ")) {
							continue
						}
					case "filter":
						if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[4:], " ")) {
							continue
						}
					case "find":
						if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[4:], " ")) {
							continue
						}
					case "exclude":
						if strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[4:], " ")) {
							continue
						}
					default:
						if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[3:], " ")) {
							continue
						}
					}
				} else {
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[3:], " ")) {
						continue
					}
				}
				details_filtered = append(details_filtered, invoke)
			} else {
				details_filtered = append(details_filtered, invoke)
			}
		}
	}

	return
}
