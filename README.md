# Batch Downloader (Golang)

A parallel downloader for millions files.

## Usage

```sh
./download [url-txt] [dest-dir]
```

where your `url.txt` contains

```text
https://...../0001.jpg
https://...../0045.png
https://...../0132.zip
..
```

You can exit by `Ctrl+c` or `q`.

## Warning

The filenames are preserved.
Make sure your list contains no duplicate filenames.

## Resume

Intermediate files are suffixed with '_'. For example, `0001.jpg` is saved as `0001.jpg_`.

If the downloader finds`0001.jpg`, it skips the downloading. If`0001.jpg\_` exists, it is overwritten.
