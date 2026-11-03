package v1alpha1

// Hub marks these types as the storage version for conversion. When v1beta1
// is introduced, its ConvertTo/ConvertFrom methods will target these types.
func (*ZeusRunbook) Hub()            {}
func (*ZeusApplicationPackage) Hub() {}
