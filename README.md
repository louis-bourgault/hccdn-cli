A CLI for the hack club CDN.
For my website, I wanted to make a photography portfolio page, but it was annoying getting everything uploaded because the CDN website isn't great for multiple things.

# Usage

First build with ```go build .``` or download the prebuilt binary (mac arm64 or linux amd64) from the Github Releases page. Add it to your PATH to use it anywhere on your computer, or alias the location in your zshrc or equivalent.

You need to set an environment variable of HCCDN_API_KEY. Add this to your env, or permanently to something like zshrc on mac or bashrc/zshrc/fshrc on linux.

At present, the Github Actions builds don't work on windows, because the toolchain for CGO is really annoying and complicated on windows. If you use windows, go to https://endeavouros.com/, download an iso, prepare a live usb, and wipe your entire drive and replace it with linux. Alternatively, use wsl.

## Upload file or folder

```./hccdn-cli up path/to/file/or/directory```

This will update, once everything is processed and uploaded, a JSON array of all the files and optimised versions, and a session ID (which can be used for deletion, as described below)

### Optimise
You can pass the --optimise flag or shorthand -o to optimise images before uploading them to the cdn. This works for PNG and JPEG files. This flag takes a comma seperated list of resolutions, where none means a copy which is not optimised at all, and full means originial resolution but transformed to a 85% quality webp. Apart from that, any other resolutions given specify the max width/height of an image (whatever is higher). It should be noted that images won't be upsampled, and if a specified resolution is higher than the original, the image will just not be processed to this resolution.

Syntax example: ```hccdn-cli up ./imgs/ -o="none,full,300"```

## Deletion
There is a ```rm``` command available for deleting any uploads. This can take different arguments as to what you are deleting. These options are:
- all - deletes all uploads that are in the current database.
- session id - pass any session id to delete everything that was uploaded in that specific session. This can be useful for a quick undo command to get rid of the files that you just uploaded if you do it accidentally.
- filepath - provide the path to a file or directory, and all uploads of that file/direc are deleted, including all optimisations.

Note: all directory operations are non recursive. They only affect the files immediately in that directory.

# Database

All information about files that have been uploaded are stored in:
- Macos/Windows - ```os.UserConfigDir()/hccdn-cli/hccdn.db```
- Linux: ```($XDG_CONFIG_HOME)/hccdn-cli/hccdn.db``` 
- If you don't have XDG_COMFG_HOME set, it'll store at your home directory + ```/.local/share/hccdn-cli/hccdn.db```

# Building and Releasing
This is built with github actions, and crosscompiled for Mac, Linux and Windows with Goreleaser. This action triggers on any git tag. pushed to github.

```git tag vX.Y.Z; git push --tags```

# Licence
MIT! Feel free to use it however you want, with attribution.
