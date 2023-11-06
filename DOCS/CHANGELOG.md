## CHANGELOG

###2.0.0-alpha.1

Gnomon version 2, alpha release 1.

* bbolt db support added
* gnomonindexer and gnomonsc separated into individual [Applications](https://github.com/civilware/Gnomon/tree/main/cmd)
* parallel block indexing
* multi-string support for search filter
* cleaner global var definitions and references through structures package
* logrus logging added
* utilized [Diff()](https://github.com/deroproject/graviton/blob/master/diff_tree.go#L26) in support of #9 to optimize diffing the scid variables at each index. This significantly reduced local storage bloat
* pull request template, bug and feature request templates
* optimizations throughout

###1.0.0.

* Gnomon Implemented