A CLI for the hack club CDN.
For my website, I wanted to make a photography portfolio page, but it was annoying getting everything uploaded because the CDN website isn't great for multiple things.

# Usage

First build with ```go build .```

You need to set an environment variable of HCCDN_API_KEY. Add this to your env, or permanently to something like zshrc on mac or bashrc/zshrc/fshrc on linux.

At present, doesn't work on windows. I don't like windows.

## Upload file or folder

```./hccdn-cli up path/to/file/or/directory```

This will update, once everything is processed and uploaded, a JSON array of all the files and optimised versions, and a session ID (which can be used for deletion, as described below)

# Building and Releasing
This is built with github actions, and crosscompiled for Mac, Linux and Windows with Goreleaser. This action triggers on any git tag. pushed to github.

```git tag vX.Y.Z; git push --tags```

# Licence
MIT! Ideally, I'd love for this to be a part of the main hack club cli system, since i think its really useful and cool


# Todo
- Check whether something already exists when uploading it
- expand readme