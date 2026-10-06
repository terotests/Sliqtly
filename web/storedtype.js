// The content types a deck's files are kept under in Storage: the list
// storage.rules allows (storedType there, the same string). Anything else
// (a web page, a script) is kept as application/octet-stream, so a share's
// files cannot be a page served from the bucket's address; the file's own
// type stays in the share's list, where the viewer reads it.
export const STORED_TYPES =
  "(image/(png|jpeg|gif|webp|avif|bmp|svg[+]xml)|audio/[-a-z0-9.+]+|video/[-a-z0-9.+]+|text/(plain|csv|markdown|tab-separated-values)|application/(octet-stream|json|pdf|zip|vnd[.]ms-excel|vnd[.]openxmlformats-officedocument[.][-a-z0-9.+]+|vnd[.]oasis[.]opendocument[.][-a-z0-9.+]+|vnd[.]sliqtly[.][-a-z0-9.+]+))(;.*)?";

const allowed = new RegExp(`^(?:${STORED_TYPES})$`);

/** The type to keep a file under: its own when the rules take it. */
export function storedType(type) {
  return type && allowed.test(type) ? type : "application/octet-stream";
}
