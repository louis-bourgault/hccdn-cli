A CLI for the hack club CDN.
For my website, I wanted to make a photography portfolio page, but it was annoying getting everything uploaded because the CDN website isn't great for multiple things.

# Usage

First build with ```go build .```

## Upload file or folder

```./hccdn-cli up path/to/file/or/directory```

This will update, once everything is processed and uploaded, a JSON array of all the files and optimised versions, and a session ID (which can be used for deletion, as described below)


# Todo
- Check whether something already exists when uploading it
- expand readme