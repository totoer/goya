package tsfrontend

import "slices"

const HelperIdentifierPrefix = "\x00goya.helper."

// Native helpers are language implementation details, loaded by private VM
// operations rather than installed as globals or parsed from emitted JS text.
var nativeHelpers = map[string]string{
	"typescript:esDecorate":                      "__esDecorate",
	"typescript:runInitializers":                 "__runInitializers",
	"typescript:propKey":                         "__propKey",
	"typescript:setFunctionName":                 "__setFunctionName",
	"typescript:decorate":                        "__decorate",
	"typescript:metadata":                        "__metadata",
	"typescript:param":                           "__param",
	"typescript:classPrivateFieldGet":            "__classPrivateFieldGet",
	"typescript:classPrivateFieldSet":            "__classPrivateFieldSet",
	"typescript:classPrivateFieldIn":             "__classPrivateFieldIn",
	"typescript:await":                           "__await",
	"typescript:asyncGenerator":                  "__asyncGenerator",
	"typescript:asyncDelegator":                  "__asyncDelegator",
	"typescript:asyncValues":                     "__asyncValues",
	"typescript:awaiter":                         "__awaiter",
	"goya:superIndex":                            "__superIndex",
	"typescript:assign":                          "__assign",
	"typescript:rest":                            "__rest",
	"typescript:makeTemplateObject":              "__makeTemplateObject",
	"typescript:addDisposableResource":           "__addDisposableResource",
	"typescript:disposeResources":                "__disposeResources",
	"typescript:commonjscreatebinding":           "__createBinding",
	"typescript:commonjscreatevalue":             "__setModuleDefault",
	"typescript:commonjsimportstar":              "__importStar",
	"typescript:commonjsimportdefault":           "__importDefault",
	"typescript:export-star":                     "__exportStar",
	"typescript:rewriteRelativeImportExtensions": "__rewriteRelativeImportExtension",
}

func scopedHelper(name string) bool {
	return name == "typescript:async-super" || name == "typescript:advanced-async-super"
}

func NativeHelperNames() []string {
	names := make([]string, 0, len(nativeHelpers))
	for _, name := range nativeHelpers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
