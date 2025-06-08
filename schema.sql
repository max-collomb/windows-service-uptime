-- Schéma pour la base de données de monitoring Windows
-- À exécuter dans votre base PostgreSQL

-- Création de la table events dans le schéma public
CREATE TABLE IF NOT EXISTS public.events (
    id SERIAL PRIMARY KEY,
    at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    host CHAR(6) NOT NULL,
    evt CHAR(3) NOT NULL CHECK (evt IN ('on', 'off'))
);

-- Index pour améliorer les performances des requêtes
CREATE INDEX IF NOT EXISTS idx_events_host_at ON public.events(host, at);
CREATE INDEX IF NOT EXISTS idx_events_at ON public.events(at);

-- Commentaires pour documenter la table
COMMENT ON TABLE public.events IS 'Journal des événements de démarrage/arrêt des machines Windows';
COMMENT ON COLUMN public.events.id IS 'Identifiant unique auto-incrémenté';
COMMENT ON COLUMN public.events.at IS 'Timestamp de l''événement';
COMMENT ON COLUMN public.events.host IS 'Nom de la machine (6 char max : lt-aml | lt-flo | dk-tom | dk-max | lt-old)';
COMMENT ON COLUMN public.events.evt IS 'Type d''événement: "on" pour démarrage/réveil, "off" pour arrêt/veille';

-- Exemple de création d'un utilisateur dédié (optionnel)
-- CREATE USER monitor_user WITH PASSWORD 'your_secure_password';
-- GRANT INSERT, SELECT, DELETE ON public.events TO monitor_user;
-- GRANT USAGE, SELECT ON SEQUENCE public.events_id_seq TO monitor_user;