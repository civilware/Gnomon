package indexer

import (
	"fmt"
	"strings"

	"github.com/civilware/Gnomon/structures"
)

func SplitLineParts(line_parts []string) (filt_line_parts []string) {
	var filt_str string

	strjoin := strings.Join(line_parts[:], " ")
	idx := strings.Index(strjoin, "|")

	if idx > 0 {
		filt_str = strjoin[idx:]
	} else {
		filt_str = strjoin[0:]
	}

	return strings.Split(filt_str, " ")
}

func (indexer *Indexer) PipeFilter(line_parts []string, invokedetails []*structures.SCTXParse) (details_filtered []*structures.SCTXParse) {
	for _, invoke := range invokedetails {
		if line_parts[0] == "|" {
			if len(line_parts) > 1 {
				// Here we assume there's some modern language usage to filter data which we can manipulate *how* we sort data
				// Example: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
				// Note - Includes references on indexes to d6ad... of string a9bf71...
				// Example2: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | grep a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
				// Note - Includes references on indexes to d6ad... of string a9bf71...
				// Example3: listscinvoke_byscid d6ad66e39c99520d4ed42defa4643da2d99f297a506d3ddb6c2aaefbe011f3dc | exclude a9bf71fb8561758cd7c5d7507516e05df4fba6c6e7240f9086b7783cfd8a648e
				// Note - Excludes references on indexes to d6ad... of string a9bf71...
				// TODO: More elegant + various data type sifting and other. Reference - https://github.com/deroproject/derohe/blob/main/cmd/dero-wallet-cli/easymenu_post_open.go#L290
				switch line_parts[1] {
				case "grep":
					lp := 2
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
				case "filter":
					lp := 2
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
				case "find":
					lp := 2
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
				case "exclude":
					lp := 2
					if strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) || strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) || strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
				default:
					lp := 1
					if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
						continue
					}
				}
			} else {
				lp := 1
				if !strings.Contains(fmt.Sprintf("%v", invoke.Sc_args), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Sender), strings.Join(line_parts[lp:], " ")) && !strings.Contains(fmt.Sprintf("%v", invoke.Txid), strings.Join(line_parts[lp:], " ")) {
					continue
				}
			}
			details_filtered = append(details_filtered, invoke)
		} else {
			// Catch all append if no conditions are matched, simply output the expected details that were input to this func
			details_filtered = append(details_filtered, invoke)
		}
	}

	return
}
