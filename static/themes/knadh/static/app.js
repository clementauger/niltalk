const linkifyExpr = /(\b(https?|ftp|file):\/\/[-A-Z0-9+&@#\/%?=~_|!:,.;]*[-A-Z0-9+&@#\/%=~_|])/ig;
const notifType = {
    notice: "notice",
    error: "error"
};
const typingDebounceInterval = 3000;

Vue.component("expand-link", {
    props: ["link"],
    data: function () {
        return {
            visible: false
        }
    },
    methods: {
        select(e) {
            e.target.select();
        }
    },
    template: `
        <div class="expand-link">
            <a href="#" v-on:click.prevent="visible = !visible">🔗</a>
            <input v-if="visible" v-on:click="select" readonly type="text" :value="link" />
        </div>
    `
});

var commands = {
  "growl": {
    "help": "Send a growl notification to an user",
    "usage": "/growl [user] [message]",
  },
  "ping": {
    "help": "Send a ping notification to an user",
    "usage": "/ping [user] [message]",
  },
  "whisper": {
    "help": "Send a message to a specific user",
    "usage": "/whisper [user] [message]",
  },
  "help": {
    "help": "Show commands help",
    "usage": "/help [command]?",
  },
}

// throw it at startup, though you will need an ssl certificate.
Notify.requestPermission(null, null);

var converter = new showdown.Converter();

var app = new Vue({
    el: "#app",
    delimiters: ["{(", ")}"],
    data: {
        isBusy: false,
        chatOn: false,
        sidebarOn: true,
        disposed: false,
        hasSound: true,

        // Global flash / notifcation properties.
        notifTimer: null,
        notifMessage: "",
        notifType: "",

        // New activity animation in title bar. Page title is cached on load
        // to use in the animation.
        newActivity: false,
        newActivityCounter: 0,
        pageTitle: document.title,

        typingTimer: null,
        typingPeers: new Map(),

        // Form fields.
        roomName: "",
        handle: "",
        password: "",
        userpwd: "",
        message: "",

        // Chat data.
        self: {},
        messages: [],
        peers: [],

        // upload
        isDraggingOver: false,
    },
    created: function () {
        this.initClient();
        this.initTimers();

        if (window.hasOwnProperty("_room") && _room.auth) {
            this.toggleChat();
            Client.init(_room.id);
            Client.connect();
        }
    },
    computed: {
        Client() {
            return window.Client;
        }
    },
    methods: {
        // Handle room creation.
        handleCreateRoom() {
            fetch("/api/rooms", {
                method: "post",
                body: JSON.stringify({
                    name: this.roomName,
                    password: this.password
                }),
                headers: { "Content-Type": "application/json; charset=utf-8" }
            })
                .then(resp => resp.json())
                .then(resp => {
                    this.toggleBusy();
                    if (resp.error) {
                        this.notify(resp.error, notifType.error);
                    } else {
                        document.location.replace("/r/" + resp.data.id);
                    }
                })
                .catch(err => {
                    this.toggleBusy();
                    this.notify(err, notifType.error);
                });
        },

        // Login to a room.
        handleLogin() {
            const handle = this.handle.replace(/[^a-z0-9_\-\.@]/ig, "");

            this.notify("Logging in", notifType.notice);
            fetch("/r/" + _room.id + "/login", {
                method: "post",
                body: JSON.stringify({ handle: handle, password: this.password, userpwd: this.userpwd }),
                headers: { "Content-Type": "application/json; charset=utf-8" }
            })
                .then(resp => resp.json())
                .then(resp => {
                    this.toggleBusy();
                    if (resp.error) {
                        this.notify(resp.error, notifType.error);
                        // pwdField.focus();
                        return;
                    }

                    this.clear();
                    this.deNotify();
                    this.toggleChat();
                    Client.init(_room.id);
                    Client.connect();
                })
                .catch(err => {
                    this.toggleBusy();
                    this.notify(err, notifType.error);
                });
        },

        // Capture keypresses to send message on Enter key and to broadcast
        // "typing" statuses.
        handleChatKeyPress(e) {
            if (e.keyCode == 13 && !e.shiftKey) {
                e.preventDefault();
                this.handleSendMessage();
                return;
            }

            // If it's a non "text" key, ignore.
            if (!String.fromCharCode(e.keyCode).match(/(\w|\s)/g)) {
                return;
            }

            // Debounce and wait for N seconds before sending a typing status.
            if (this.typingTimer) {
                return;
            }

            // Send the 'typing' status.
            Client.sendMessage(Client.MsgType["typing"]);

            this.typingTimer = window.setTimeout(() => {
                this.typingTimer = null;
            }, typingDebounceInterval);
        },

        handleSendMessage() {
          window.clearTimeout(this.typingTimer);
          this.typingTimer = null;

          var msg = this.message.trim();
          if (msg.length<1) {
            return
          }
          this.message = "";

          //lookup for a command
          var commandName = "";
          Object.keys(commands).map((key)=>{
            var re = new RegExp("^(/"+key+")(\\s+|$)");
            if (msg.match(re)) {
              commandName = key
            }
          });

          // no command provided, handle a regular message
          if (commandName.length<1) {
            Client.sendMessage(Client.MsgType["message"], msg);

          }else if (commandName=="help"){
            var message = "";
            var re = new RegExp("^(/"+commandName+")\\s+([^\\s]+)");
            var matches = msg.match(re);
            if (matches) {
              var key = matches[2]
              message += "<b>/" + key + "</b>: "+commands[key].help+"<br/>"
              message += "Usage " + commands[key].usage+"<br/>"
            } else{
              message += "<b>Help for all commands</b><br/>"
              Object.keys(commands).map((key)=>{
                message += "<br/>"
                message += "<b>/"+key +"</b>: "+commands[key].help+"<br/>"
                message += "Usage: " + commands[key].usage+"<br/>"
              });
            }
            this.messages.push({
                type: Client.MsgType["help"],
                message: message
            });
            this.scrollToNewester();

          }else if (commandName=="growl"){
            var re = new RegExp("^(/"+commandName+")\\s+([^\\s]+)\\s+(.*)");
            var matches = msg.match(re);
            Client.sendMessage(Client.MsgType["growl"], {to:matches[2], msg:matches[3],from: this.self.handle});

          }else if (commandName=="ping"){
            var re = new RegExp("^(/"+commandName+")\\s+([^\\s]+)(\\s+.*)?");
            var matches = msg.match(re);
            Client.sendMessage(Client.MsgType["ping"], {to:matches[2], msg:matches[3],from: this.self.handle});

          }else if (commandName=="whisper"){
            var re = new RegExp("^(/"+commandName+")\\s+([^\\s]+)(\\s+.*)?");
            var matches = msg.match(re);
            Client.sendMessage(Client.MsgType["whisper"], {to:matches[2], msg:matches[3],from: this.self.handle});

          }
        },

        handleLogout() {
            if (!confirm("Logout?")) {
                return;
            }
            fetch("/r/" + _room.id + "/login", {
                method: "delete",
                headers: { "Content-Type": "application/json; charset=utf-8" }
            })
                .then(resp => resp.json())
                .then(resp => {
                    this.toggleChat();
                    document.location.reload();
                })
                .catch(err => {
                    this.notify(err, notifType.error);
                });
        },

        handleDisposeRoom() {
            if (!confirm("Disconnect all peers and destroy this room?")) {
                return;
            }
            Client.sendMessage(Client.MsgType["room.dispose"]);
        },

        // Flash notification.
        notify(msg, typ, timeout) {
            clearTimeout(this.notifTimer);
            this.notifTimer = setTimeout(function () {
                this.notifMessage = "";
                this.notifType = "";
            }.bind(this), timeout ? timeout : 3000);

            this.notifMessage = msg;
            if (typ) {
                this.notifType = typ;
            }
        },

        beep() {
            const b = document.querySelector("#beep");
            b.pause();
            b.load();
            b.play().catch((e)=>{});
        },

        deNotify() {
            clearTimeout(this.notifTimer);
            this.notifMessage = "";
            this.notifType = "";
        },

        hashColor(str) {
            for (var i = 0, hash = 0; i < str.length; hash = str.charCodeAt(i++) + ((hash << 5) - hash));
            for (var i = 0, colour = "#"; i < 3; colour += ("00" + ((hash >> i++ * 8) & 0xFF).toString(16)).slice(-2));
            return colour;
        },

        formatDate(ts) {
            var t = new Date(ts),
                h = t.getHours(),
                minutes = t.getMinutes(),
                hours = ((h + 11) % 12 + 1);
            return (hours < 10 ? "0" : "")
                + hours.toString()
                + ":"
                + (minutes < 10 ? "0" : "")
                + minutes.toString()
                + " " + (h > 12 ? "PM" : "AM");
        },

        formatMessage(text) {
            const div = document.createElement("div");
            div.appendChild(document.createTextNode(text));
            var html = div.innerHTML;
            var links = html.match(linkifyExpr)
            links && links.map((l)=>{
              var match = null;
              // lookup for some video integration.
              //https://www.youtube.com/watch?v=kgQEvTujCoE
              var ytb = new RegExp("^(http|https)?://([^/]+)youtube([^/]+)/watch?.*v=([^&]+)[^\\s]*","gi")
              match = html.match(ytb)
              if (match) {
                ytb = new RegExp("^(http|https)?://([^/]+)youtube([^/]+)/watch?.*v=([^&]+)[^\\s]*","i")
                match.map((m)=>{
                  var id = "";
                  var captured = m.match(ytb)
                  if (captured) {
                    id = captured[4]
                  }
                  if (id) {
                    html = html.replace(m,
                      `<iframe type="text/html"
                      class="video"
                      webkitallowfullscreen mozallowfullscreen allowfullscreen
                      src="http://www.youtube.com/embed/${id}?enablejsapi=1"
                      ></iframe>`.replace(/\n+/ig," "))
                  }
                })
                return
              }
              //https://www.dailymotion.com/video/x7vzvxe
              var dlm = new RegExp("^(http|https)?://([^/]+)dailymotion([^/]+)/video/([^?/]+)[^\\s]*","gi")
              match = html.match(dlm)
              if (match) {
                dlm = new RegExp("^(http|https)?://([^/]+)dailymotion([^/]+)/video/([^?/]+)","i")
                match.map((m)=>{
                  var id = "";
                  var captured = m.match(dlm)
                  if (captured) {
                    id = captured[4]
                  }
                  if (id) {
                    html = html.replace(m,
                      `<iframe src="https://www.dailymotion.com/embed/video/${id}"
                        class="video"
                        webkitallowfullscreen mozallowfullscreen allowfullscreen
                        ></iframe>`.replace(/\n+/ig," "))
                  }
                })
                return
              }
              //https://vimeo.com/220643959
              var vmo = new RegExp("^(http|https)?://([^/]*)vimeo([^/]+)/([^?/\\s]+)","gi")
              match = html.match(vmo)
              if (match) {
                vmo = new RegExp("^(http|https)?://([^/]*)vimeo([^/]+)/([^?/\\s]+)","i")
                match.map((m)=>{
                  var id = "";
                  var captured = m.match(vmo)
                  if (captured) {
                    id = captured[4]
                  }
                  if (id) {
                    html = html.replace(m,
                      `<iframe src="//player.vimeo.com/video/${id}?title=0&byline=0"
                        class="video"
                        webkitallowfullscreen mozallowfullscreen allowfullscreen
                        ></iframe>`.replace(/\n+/ig," "))
                  }
                })
                return
              }
              //https://peertube.social/videos/watch/ad395c9b-9702-4060-ac05-4c94b64956ab
              var ptb = new RegExp("^(http|https)?://([^/]*)peertube([^/]+)/videos/watch/([^?/\\s]+)$","gi")
              match = html.match(ptb)
              if (match) {
                ptb = new RegExp("^(http|https)?://([^/]*)peertube([^/]+)/videos/watch/([^?/\\s]+)$","i")
                match.map((m)=>{
                  var id = "";
                  var sdns = "";
                  var captured = m.match(ptb)
                  if (captured) {
                    sdns = captured[3]
                    id = captured[4]
                  }
                  if (id && sdns) {
                    html = html.replace(m,
                      `<iframe src="https://peertube${sdns}/videos/embed/${id}"
                        class="video"
                        sandbox="allow-same-origin allow-scripts"
                        webkitallowfullscreen mozallowfullscreen allowfullscreen
                        ></iframe>`.replace(/\n+/ig," "))
                  }
                })
                return
              }
              // otherwise it is a regular link
              html = html.replace(l, `<a refl='noopener noreferrer' href='${l}' target='_blank'>${l}</a>`)
            })
            return converter.makeHtml(html)
            // return html.replace(/\n+/ig, "<br />");
        },

        scrollToNewester() {
            this.$nextTick().then(function () {
              if (this.$refs["messages"]) {
                var el = this.$refs["messages"].querySelector(".message:last-child")
                if (el) {
                  el.scrollIntoView();
                }
              }
            }.bind(this));
        },

        // Toggle busy (form button) state.
        toggleBusy() {
            this.isRequesting = !this.isRequesting;
        },

        toggleSidebar() {
            this.sidebarOn = !this.sidebarOn;
        },

        toggleChat() {
            this.chatOn = !this.chatOn;

            this.$nextTick().then(function () {
                if (!this.chatOn && this.$refs["form-password"]) {
                    this.$refs["form-password"].focus();
                    return
                }
                if (this.$refs["form-message"]) {
                    this.$refs["form-message"].focus();
                    this.onResize();
                }
            }.bind(this));
        },

        // Clear all states.
        clear() {
            this.handle = "";
            this.password = "";
            this.password = "";
            this.message = "";
            this.self = {};
            this.messages = [];
            this.peers = [];
        },

        // WebSocket client event handlers.
        onConnect() {
            Client.getPeers();
        },

        onDisconnect(typ) {
            switch (typ) {
                case Client.MsgType["disconnect"]:
                    this.notify("Disconnected. Retrying ...", notifType.notice);
                    break;

                case Client.MsgType["peer.ratelimited"]:
                    this.notify("You sent too many messages", notifType.error);
                    this.toggleChat();
                    break;

                case Client.MsgType["room.full"]:
                    this.notify("Room is full", notifType.error);
                    this.toggleChat();
                    break;

                case Client.MsgType["room.dispose"]:
                    this.notify("Room disposed", notifType.error);
                    this.toggleChat();
                    this.disposed = true;
                    break;
            }
            // window.location.reload();
        },

        onReconnecting(timeout) {
            this.notify("Disconnected. Retrying ...", notifType.notice, timeout);
        },

        onPeerSelf(data) {
            this.self = {
                ...data.data,
                avatar: this.hashColor(data.data.id)
            };
        },

        onPeerJoinLeave(data, typ) {
            const peer = data.data;
            let peers = JSON.parse(JSON.stringify(this.peers));

            // Add / remove the peer from the existing list.
            if (typ === Client.MsgType["peer.join"]) {
                peers.push(peer);
            } else {
                peers = peers.filter((e) => { return e.id !== peer.id; });
            }
            this.onPeers(peers);

            // Notice in the message area;
            peer.avatar = this.hashColor(peer.id);
            if (peer.id!==this.self.id){
              this.messages.push({
                  type: typ,
                  peer: peer,
                  timestamp: data.timestamp
              });
            }
            this.scrollToNewester();
        },

        onPeers(data) {
            const peers = data.sort(function (a, b) {
                if (a.handle < b.handle) {
                    return -1;
                } else if (a.handle > b.handle) {
                    return 1;
                } else {
                    return 0;
                }
            });

            peers.forEach(p => {
                p.avatar = this.hashColor(p.id);
            });

            this.peers = peers;
        },

        onTyping(data) {
            if (data.data.id === this.self.id) {
                return;
            }
            this.typingPeers.set(data.data.id, { ...data.data, time: Date.now() });
            this.$forceUpdate();
        },

        onMessage(data) {
            this.typingPeers.delete(data.data.peer_id);
            this.messages.push({
                type: data.type,
                timestamp: data.timestamp,
                message: data.data.message,
                peer: {
                    id: data.data.peer_id,
                    handle: data.data.peer_handle,
                    avatar: this.hashColor(data.data.peer_id)
                }
            });
            this.scrollToNewester();
            // If the window isn't in focus, start the "new activity" animation
            // in the title bar.
            if (!document.hasFocus()) {
                this.newActivity = true;
                this.beep();
            }
        },

        onUpload(data) {
          var d = data.data.data;
          if (data.type==Client.MsgType["uploading"]) {
            var found = false;
            this.messages.map((m) => {
              if (m.uid===d.uid){
                m.files=d.files;
                m.percent=d.percent;
                m.type=data.type;
                found=true;
              }
            });
            if(!found) {
              this.messages.push({
                type: data.type,
                timestamp: data.timestamp,
                uid: d.uid,
                files: d.files,
                percent: d.percent,
                peer: {
                  id: data.data.peer_id,
                  handle: data.data.peer_handle,
                  avatar: this.hashColor(data.data.peer_id)
                }
              });
            }
          }else {
            var found = false;
            this.messages.map((m) => {
              if (m.uid===d.uid){
                if(d.res) {
                  m.res=d.res.data;
                }
                m.files = m.files || [];
                m.err=d.err;
                m.type=data.type;
                found=true;
              }
            });
            if(!found) {
              var res = {};
              if (d.res) {
                res = d.res.data;
              }
              this.messages.push({
                type: data.type,
                timestamp: data.timestamp,
                uid: d.uid,
                res: res,
                files: [],
                err: d.err,
                peer: {
                  id: data.data.peer_id,
                  handle: data.data.peer_handle,
                  avatar: this.hashColor(data.data.peer_id)
                }
              });
            }
          }
          this.scrollToNewester();
        },

        onPing(data) {
          if (!document.hasFocus()) {
            var from = data.data.data.from;
            var msg = data.data.data.msg;
            if(!msg){return}

            if (!Notify.needsPermission) {
              var title = from+" pings you!";
              new Notify(title, {
                body: msg,
                tag: $.uniqueId(),
                timeout: 4
              }).show();
            }else{
              this.messages.push({
                type: Client.MsgType["ping"],
                message: msg,
                timestamp: data.timestamp,
                peer: {
                    id: data.data.peer_id,
                    handle: data.data.peer_handle,
                    avatar: this.hashColor(data.data.peer_id)
                }
              });
              this.scrollToNewester();
              this.newActivity = true;
              this.beep();
            }
          }
        },

        onWhisper(data) {
          var msg = data.data.data.msg;
          if (msg) {
            this.messages.push({
              type: Client.MsgType["whisper"],
              message: msg,
              timestamp: data.timestamp,
              peer: {
                  id: data.data.peer_id,
                  handle: data.data.peer_handle,
                  avatar: this.hashColor(data.data.peer_id)
              }
            });
            this.scrollToNewester();
            if (!document.hasFocus()) {
              this.newActivity = true;
              this.beep();
            }
          }
        },

        // Register chat client events.
        initClient() {
            Client.on(Client.MsgType["connect"], this.onConnect);
            Client.on(Client.MsgType["disconnect"], (data) => { this.onDisconnect(Client.MsgType["disconnect"]); });
            Client.on(Client.MsgType["peer.ratelimited"], (data) => { this.onDisconnect(Client.MsgType["peer.ratelimited"]); });
            Client.on(Client.MsgType["room.dispose"], (data) => { this.onDisconnect(Client.MsgType["room.dispose"]); });
            Client.on(Client.MsgType["room.full"], (data) => { this.onDisconnect(Client.MsgType["room.full"]); });
            Client.on(Client.MsgType["reconnecting"], this.onReconnecting);

            Client.on(Client.MsgType["peer.info"], this.onPeerSelf);
            Client.on(Client.MsgType["peer.list"], (data) => { this.onPeers(data.data); });
            Client.on(Client.MsgType["peer.join"], (data) => { this.onPeerJoinLeave(data, Client.MsgType["peer.join"]); });
            Client.on(Client.MsgType["peer.leave"], (data) => { this.onPeerJoinLeave(data, Client.MsgType["peer.leave"]); });
            Client.on(Client.MsgType["message"], this.onMessage);
            Client.on(Client.MsgType["motd"], this.onMessage);
            Client.on(Client.MsgType["uploading"], this.onUpload);
            Client.on(Client.MsgType["upload"], this.onUpload);
            Client.on(Client.MsgType["typing"], this.onTyping);
            Client.on(Client.MsgType["ping"], this.onPing);
            Client.on(Client.MsgType["whisper"], this.onWhisper);
        },

        initTimers() {
            // Title bar "new activity" animation.
            window.setInterval(() => {
                if (!this.newActivity) {
                    return;
                }
                if (this.newActivityCounter % 2 === 0) {
                    document.title = "[•] " + this.pageTitle;
                } else {
                    document.title = this.pageTitle;
                }
                this.newActivityCounter++;
            }, 2500);
            window.onfocus = () => {
                this.newActivity = false;
                document.title = this.pageTitle;
            };

            // Sweep "typing" statuses at regular intervals.
            window.setInterval(() => {
                let changed = false;
                this.typingPeers.forEach((p) => {
                    if ((p.time + typingDebounceInterval) < Date.now()) {
                        this.typingPeers.delete(p.id);
                        changed = true;
                    }
                });
                if (changed) {
                    this.$forceUpdate();
                }
            }, typingDebounceInterval);
        },

        dragEnter(e) {
          this.isDraggingOver=true
        },

        dragLeave(e) {
          this.isDraggingOver=false
        },

        // image upload
        addFile(e) {
          this.isDraggingOver=false
          // based on https://www.raymondcamden.com/2019/08/08/drag-and-drop-file-upload-in-vuejs
          let droppedFiles = e.dataTransfer.files;
          if(!droppedFiles) return;
          var uid = Math.round(new Date().getTime() + (Math.random() * 100));
          // this tip, convert FileList to array, credit: https://www.smashingmagazine.com/2018/01/drag-drop-file-uploader-vanilla-js/
          var ok = true;
          let formData = new FormData();
          var files = [];
          ([...droppedFiles]).forEach((f,x) => {
            if (x>=20) {
              this.notify("Too much files to upload", notifType.error);
              ok = false;
              return
            }
            formData.append('file'+(x), f);
            files.push(f.name)
          })
          if (!ok) {
            return
          }
          Client.sendMessage(Client.MsgType["uploading"], {uid:uid,files:files,percent:0});

          axios.post("/r/" + _room.id + "/upload", formData,
            {
              headers: {
                  'Content-Type': 'multipart/form-data'
              },
              onUploadProgress: function( progressEvent ) {
                var p = parseInt( Math.round( ( progressEvent.loaded / progressEvent.total ) * 100 ) );
                Client.sendMessage(Client.MsgType["uploading"], {uid:uid,files:files,percent:p});
              }
            }
          ).then(res => {
            if (res.error){
              this.notify(res.error, notifType.error);
              Client.sendMessage(Client.MsgType["upload"], {uid:uid,err:res.error});
            }else{
              Client.sendMessage(Client.MsgType["upload"], {uid:uid,res:res.data});
            }
          })
          .catch(err => {
            Client.sendMessage(Client.MsgType["upload"], {uid:uid,err:err.message});
            this.notify(err, notifType.error);
          });
        },

        onResize(event) {
          window.requestAnimationFrame(()=>{
            var header = document.querySelector(".header");
            var style = getComputedStyle(header)
            var headerHeight = parseInt(style.marginTop) + parseInt(style.marginBottom) + header.offsetHeight;
            var fc = document.querySelector(".form-chat")
            var c = document.querySelector(".chat .messages")
            if(fc && c) {
              var vph = window.innerHeight;
              var h = vph-(fc.offsetHeight + headerHeight);
              if (h<0) { h = 0;}
              c.style.height = h + "px";
            }
          });
        }
    },
    mounted() {
      window.addEventListener('resize', this.onResize)
    },
    beforeDestroy() {
      window.removeEventListener('resize', this.onResize)
    }
});
