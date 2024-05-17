const yaml = require('yaml');
const path = require('path');

// TopicContentsFragment contains data necessary to generate a table of contents
// page for the subdirectories of a user-specified root directory.
// @param fs - The filesystem to use. Either memfs or the NodeJS fs package.
// @param {string} root - file path in fs in which to place the table of
// contents partial. While TopicContentsFragment is not responsible for writing
// the output file, it builds the table of contents based on the assumption that
// it will be a partial in the specified directory.
class TopicContentsFragment {
  constructor(command, fs, root) {
    this.command = command;
    this.fs = fs;
    this.root = root;
  }
  // makeTopicPage constructs an index of the files in the directory, returning
  // it as a string.
  //
  // makeTopicPage expects files in the directory to be MDX files with two
  // frontmatter keys, title and description. For each subdirectory, there must
  // be a menu page in the same directory as the subdirectory with the same name
  // as the subdirectory. For example, if there is a subdirectory called
  // "guides", we expect a menu page called "guides.mdx". We use this to
  // provide context for the subdirectory in the table of contents.
  makeTopicPage() {
    const parts = path.parse(this.root);
    const rootConfig = path.join(parts.dir, parts.name + '.yaml');
    const fm = this.getFrontmatter(rootConfig);
    const initial = `---
title: ${fm.title}
description: ${fm.description}
---

{/*GENERATED FILE. DO NOT EDIT. RECREATE WITH THIS COMMAND:
${this.command}*/}

${fm.description}

`;
    return this.addTopicsFromDir(this.root, initial, 0);
  }

  // getFrontmatter returns the frontmatter YAML of an MDX page as an object
  // containing keys "title" and "description".
  getFrontmatter(fullPath) {
    let text = this.fs.readFileSync(fullPath, 'utf8');
    // Remove any initial YAML document separator
    if (text.startsWith('---\n')) {
      text = text.slice('---\n'.length);
    }
    const docs = text.split('---');
    if (docs.length === 0) {
      throw new Error(`no frontmatter in ${fullPath}`);
    }

    const fm = yaml.parse(docs[0]);
    if (!fm || !fm.hasOwnProperty('title')) {
      throw new Error(`page ${fullPath} does not have a title`);
    }

    return fm;
  }

  // relativePathToFile takes a filepath and returns a path we can use in links
  // to the file in a table of contents page. The link path is a relative path
  // to the directory where we are placing the table of contents page.
  // @param filepath {string} - the path from which to generate a link path.
  relativePathToFile(filepath) {
    const rootName = path.parse(this.root).name;
    return path.join(rootName, filepath.slice(this.root.length));
  }

  // addTopicsFromDir takes the path at dirPath and recursively adds any topic
  // listings to the string sofar. Returns the new string.
  // @param dirPath {string} - path to the directory to use for generating part
  // of the table of contents. Handles one directory level per call.
  // @param sofar {string} - the table of contents fragment generated prior to
  // this iteration.
  // @ param level {number} is the heading level at
  // which to put any topic headings.
  addTopicsFromDir(dirPath, sofar, level) {
    let newText = sofar;
    const files = this.fs.readdirSync(dirPath, 'utf8');

    // List all MDX files in the current directory and add them to a map of
    // files we'll use to build the menu page.
    const frontmatterFiles = files.reduce((accum, current) => {
      const filename = path.parse(current).base;
      if (
        !current.endsWith('.mdx') ||
        // We assume that each All Topics partial is included in a page called
        // "all-topics.mdx".
        filename == 'all-topics.mdx'
      ) {
        return accum;
      }
      accum[path.join(dirPath, current)] = true;
      return accum;
    }, {});

    // Add directory data to the menu page. We expect each directory to have
    // an accompanying YAML file, named after the directory and at the same
    // place in the file tree.
    files.forEach((current) => {
      const stats = this.fs.statSync(path.join(dirPath, current));
      if (!stats.isDirectory()) {
        return;
      }
      frontmatterFiles[path.join(dirPath, current + '.yaml')] = true;
      // If we're working from a YAML config, don't also include a previously
      // generated menu page.
      delete frontmatterFiles[path.join(dirPath, current+'.mdx')]
    });

    // Add rows to the menu page.
    Object.keys(frontmatterFiles).forEach(f => {
      let relPath = this.relativePathToFile(f);
      const fm = this.getFrontmatter(f);

      // We're using a YAML file for directory information, so change the link
      // to an MDX file. We expect there to be a menu page corresponding to the
      // YAML config anyway.
      if (relPath.endsWith('.yaml')) {
        relPath = relPath.replace(/\.yaml$/, '.mdx');
      }

      newText = newText + `- [${fm.title}](${relPath}): ${fm.description}\n`;
    });

    return newText;
  }
}
module.exports.TopicContentsFragment = TopicContentsFragment;
