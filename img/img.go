package img

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"strconv"

	"github.com/chai2010/webp"
	"github.com/nfnt/resize"
)

func OptimiseImage(filePath string, qual string) (*bytes.Buffer, error) {
	input, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer input.Close()

	var output io.Writer
	output = &bytes.Buffer{}

	inputImg, _, err := image.Decode(input)
	if qual == "full" {
		err = webp.Encode(output, inputImg, &webp.Options{Lossless: false, Quality: 85})
	} else {
		qualInt, _ := strconv.Atoi(qual)
		imageWidth := inputImg.Bounds().Dx()
		imageHeight := inputImg.Bounds().Dy()
		//the inputted value is the longest side of the image, and we maintain aspect ratio
		var newWidth, newHeight int
		if imageWidth > imageHeight {
			newWidth = qualInt
			newHeight = (qualInt * imageHeight) / imageWidth
		} else {
			newHeight = qualInt
			newWidth = (qualInt * imageWidth) / imageHeight
		}
		//85% qual webp
		resizedImg := resize.Thumbnail(uint(newWidth), uint(newHeight), inputImg, resize.Lanczos3)
		err = webp.Encode(output, resizedImg, &webp.Options{Lossless: false, Quality: 85})
	}

	if err != nil {
		return nil, err
	}
	return output.(*bytes.Buffer), nil

}
