package version

// Version is the user-visible ajq version. It can be overridden at build time
// with: -ldflags "-X github.com/ricardocabral/ajq/internal/version.Version=vX.Y.Z".
var Version = "dev"
